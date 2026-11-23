package bootstrap

import (
	"crypto/rsa"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"runtime"

	"trivita-fhir/apps/fhir-server/internal/config"
	"trivita-fhir/pkg/binarystore"
	"trivita-fhir/pkg/binarystore/s3store"
)

// storage gom các phần của kho Binary đã dựng (nil khi FHIR_BINARY_STORAGE rỗng).
type storage struct {
	store  binarystore.Store
	closer func() error // nil với kho không cần đóng (S3)
	signer *binarystore.Signer
}

func (s *storage) close() error {
	if s.closer == nil {
		return nil
	}
	return s.closer()
}

// openStore mở kho theo FHIR_BINARY_STORAGE: file:<thư mục> hoặc s3:<bucket>[/<prefix>].
func openStore(s config.Settings) (binarystore.Store, func() error, error) {
	if s.StorageKind() == "s3" {
		st, err := s3store.New(s3store.Config{
			Bucket: s.S3Bucket(), Prefix: s.S3Prefix(), Region: s.S3Region, Endpoint: s.S3Endpoint,
			AccessKeyID: s.S3AccessKeyID, SecretAccessKey: s.S3SecretAccessKey, SessionToken: s.S3SessionToken, ServerSideEncryption: s.S3SSE,
		})
		return st, nil, err
	}
	fs, err := binarystore.NewFS(s.StorageDir())
	if err != nil {
		return nil, nil, err
	}
	return fs, fs.Close, nil
}

// newStorage mở kho, nạp khóa ký (tệp PEM; dev không đặt thì sinh khóa tạm có cảnh báo) và dựng Signer.
func newStorage(s config.Settings) (*storage, error) {
	st, closer, err := openStore(s)
	if err != nil {
		return nil, err
	}
	fs := &storage{store: st, closer: closer}
	key, err := loadSigningKey(s)
	if err != nil {
		_ = fs.close()
		return nil, err
	}
	var prev []*rsa.PublicKey
	for _, p := range s.StoragePreviousKeyPaths() {
		b, err := os.ReadFile(p)
		if err != nil {
			_ = fs.close()
			return nil, fmt.Errorf("FHIR_STORAGE_SIGNING_PREVIOUS_KEYS: đọc %q: %w", p, err)
		}
		pk, err := binarystore.ParsePublicKeyPEM(b)
		if err != nil {
			_ = fs.close()
			return nil, fmt.Errorf("FHIR_STORAGE_SIGNING_PREVIOUS_KEYS: %q: %w", p, err)
		}
		prev = append(prev, pk)
	}
	sg, err := binarystore.NewSigner(binarystore.SignerConfig{BaseURL: s.StoragePublicURL(), Key: key, VerifyKeys: prev, TTL: s.StorageURLTTL, LegacyGET: s.StorageLegacyGET})
	if err != nil {
		_ = fs.close()
		return nil, err
	}
	fs.signer = sg
	return fs, nil
}

func loadSigningKey(s config.Settings) (*rsa.PrivateKey, error) {
	if s.StorageSigningKeyPath == "" {
		slog.Warn("FHIR_STORAGE_SIGNING_KEY_PATH chưa đặt: dùng khóa ký tạm; URL /storage mất hiệu lực khi khởi động lại và không dùng được với nhiều replica")
		return binarystore.GenerateKey()
	}
	path := s.StorageSigningKeyPath
	st, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("FHIR_STORAGE_SIGNING_KEY_PATH: %w", err)
	}
	if runtime.GOOS != "windows" && st.Mode().Perm()&0o077 != 0 { // khóa riêng không được đọc được bởi nhóm/người khác
		msg := fmt.Sprintf("FHIR_STORAGE_SIGNING_KEY_PATH: quyền tệp %#o quá rộng, phải là 0600", st.Mode().Perm())
		if s.IsProd() {
			return nil, errors.New(msg)
		}
		slog.Warn(msg)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("FHIR_STORAGE_SIGNING_KEY_PATH: %w", err)
	}
	k, err := binarystore.ParsePrivateKeyPEM(b)
	if err != nil {
		return nil, fmt.Errorf("FHIR_STORAGE_SIGNING_KEY_PATH: %w", err)
	}
	return k, nil
}
