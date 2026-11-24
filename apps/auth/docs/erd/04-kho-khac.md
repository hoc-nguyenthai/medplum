# 4. Kho khác ngoài PostgreSQL

Ngoài schema `identity`, auth giữ trạng thái ở Redis, tệp khóa ký, khóa mã hóa trong biến môi trường, và (dev) tệp SQLite. Nguồn: `internal/infrastructure/{mfastate,jti,hubtopic,keys,secretbox}`, `pkg/fhircast/redis_store.go`, [../config.md](../config.md).

**Quy tắc chung:** `AUTH_REDIS_URL` rỗng thì mọi trạng thái dưới đây nằm **trong bộ nhớ của từng instance** (`Memory`): chỉ đúng với **một** instance (hạn mức MFA, chống dùng lại mã và `jti` nhân lên theo số instance, và mất khi khởi động lại). **Staging/production bắt buộc `AUTH_REDIS_URL`.** Redis hỏng là lỗi **fail đóng** ở mọi chức năng dưới đây (không bao giờ cho qua).

## 4.1 Khóa Redis

| Khóa (mẫu) | Giá trị | TTL | Ghi bởi | Đọc bởi | Chức năng | Khi mất hoặc Redis lỗi |
|---|---|---|---|---|---|---|
| `auth:mfa:{<userId>}:totp-enroll:<loginId>` | secret TOTP **được mời** cho cặp (user, login), dạng base32 | `AUTH_MFA_ENROLLMENT_EXPIRATION` (20 phút); `SET NX` | `mfastate.OfferGetOrCreate` (khi `login`/`mfa/status` trả `enrollUri`) | `OfferGet` khi xác nhận ở `mfa/enroll` hoặc `mfa/login-enroll` | Giữ secret sinh cho riêng phiên đăng nhập này để chỉ login nào xác nhận bằng mã đúng mới biến nó thành `User.mfaSecret`; hai login cùng đăng ký thì đúng một cái thắng | Người dùng phải bắt đầu đăng ký lại; lỗi Redis ⇒ `500` chung |
| `auth:mfa:{<userId>}:login:<loginId>` | bộ đếm số lượt thử mã MFA của login | cửa sổ `AUTH_MFA_LOGIN_EXPIRATION` (10 phút) tính từ lượt đầu (`PEXPIRE` khi đếm lên 1) | `ReserveAttempt` (script Lua nguyên tử) | cùng script | Giới hạn **5 lượt mỗi login** (`AUTH_MFA_LOGIN_ATTEMPT_LIMIT`); đặt chỗ **trước** khi biết mã đúng hay sai; lần sai thứ 5 thu hồi Login | Bộ đếm về 0: cho thử lại; Redis lỗi ⇒ `500`, **không cấp code** |
| `auth:mfa:{<userId>}:user` | bộ đếm số lượt thử của cả user | cùng cửa sổ 10 phút | `ReserveAttempt` | cùng script | Giới hạn **20 lượt mỗi user** (`AUTH_MFA_USER_ATTEMPT_LIMIT`); hai khóa login và user cùng hash tag `{userId}` để cùng slot khi dùng Redis Cluster (script đụng cả hai) | như trên |
| `auth:mfa:{<userId>}:totp:<token>` | `1` (đã dùng) | `(2 × AUTH_MFA_WINDOW + 2) × 30 giây` (mặc định cửa sổ 1 ⇒ 120 giây); `SET NX` | `ClaimTOTP` | `ClaimTOTP` | Mỗi mã TOTP dùng **một lần** mỗi user (kẻ nhìn trộm không dùng lại được mã vừa nhập); TTL bao trùm mọi bước thời gian mà mã còn hợp lệ | Mã dùng lại được trong vài chục giây; Redis lỗi ⇒ `500` |
| `auth:mfa:hit:send:<loginId>` | bộ đếm số lần gửi mã MFA qua email của login | `AUTH_MFA_EMAIL_CODE_EXPIRATION` (20 phút) | `mfastate.Hit` (gọi với khóa `send:<loginId>`) | cùng script | Giới hạn **5 lần gửi mỗi login trong 20 phút** (`AUTH_MFA_EMAIL_SEND_LIMIT`) chống dùng auth làm máy gửi thư rác; vượt ⇒ `429` kèm `rate-limit-reset` | Người dùng gửi lại được ngay; Redis lỗi ⇒ `500` |
| `auth:jti:<clientId>\|<jti>` | `1` | tới `exp` của assertion cộng 1 phút (tối thiểu 1 giây); `SET NX` | `jti.Redis.UseOnce` (khi xác thực `private_key_jwt`) | cùng lệnh | Mỗi `client_assertion` (RFC 7523) chỉ dùng **một lần**: chống phát lại assertion bị lộ | Assertion cũ dùng lại được; Redis lỗi ⇒ **lỗi (từ chối)**, không cho qua |
| `fhircast:user-topic:<subject>` | UUID topic FHIRcast ổn định của chủ thể token (`subject` = user hoặc ClientApplication) | lease 1 giờ (`fhircast.DefaultLease`); **làm mới (`EXPIRE`) mỗi lần cấp token** | `hubtopic.TopicFor` → `RedisStore.UserTopic` (`SET NX` + `GET` + `EXPIRE` trong một MULTI) | cùng hàm | Các ứng dụng của cùng một người nhận cùng `hub.topic` và tìm thấy nhau; ổn định chừng nào còn xin token ít nhất mỗi giờ | Topic mới (ứng dụng không thấy nhau); **lỗi Redis ⇒ không cấp token** (`500 server_error`) khi scope có `fhircast/` |

Tiền tố `fhircast:` do `pkg/fhircast` đặt (các khóa khác cùng tiền tố như `fhircast:ep:*`, `fhircast:p:*` thuộc `ws-gateway`, **không** phải auth). `auth` chỉ chạm `fhircast:user-topic:*`.

**Nhạy cảm:** các khóa MFA chứa id user/login (S1) và secret TOTP mời (S4, tồn tại tối đa 20 phút, bản rõ dạng base32 trong Redis); các khóa còn lại không chứa bí mật. Redis do đó cũng là kho cần bảo vệ (mạng riêng, `rediss://` khi qua mạng không tin cậy).

## 4.2 Hạn mức theo IP (không có kho)

`AUTH_RATE_LIMIT` và `AUTH_LOGIN_RATE_LIMIT` đếm **trong bộ nhớ từng instance**, không dùng Redis: nhiều instance thì hạn mức thực tế nhân lên theo số instance ([../api-contract/common.md](../api-contract/common.md) mục 7).

## 4.3 Tệp và biến môi trường

| Tài nguyên | Nội dung | Ai ghi/đọc | Nhạy cảm | Ghi chú |
|---|---|---|---|---|
| Thư mục `AUTH_SIGNING_KEY_PATH` | các khóa riêng `*.pem`, quyền `0600`; `kid` là thumbprint RFC 7638 | `auth keys generate` ghi; `auth` đọc lúc khởi động; `JWKS` chỉ công bố phần công khai | **S4** (khóa riêng) | Xoay khóa: sinh khóa mới, triển khai, đợi quá 10 phút (cache của người xác minh), đổi `AUTH_ACTIVE_KID`, xóa khóa cũ khi token cũ hết hạn. Để trống ở dev: khóa tạm trong bộ nhớ (token mất hiệu lực khi khởi động lại, chỉ một instance) |
| Biến `AUTH_MFA_SECRET_KEYS` | danh sách `kid:base64(32 byte)`; khóa đầu mã hóa, mọi khóa giải mã | vận hành đặt; `secretbox` dùng | **S4** | Mã hóa AES-256-GCM, giá trị lưu `enc:v1:<kid>:...` ở `User.mfaSecret`. **Không đặt (dev) ⇒ lưu bản rõ và ghi cảnh báo**; bắt buộc ở staging/production |
| Tệp SQLite `./auth.db` | cùng bộ bảng như PostgreSQL, **không có schema** | `auth` (chỉ dev) | S2 đến S4 | Cấm ở staging/production. Tên cũ `authen.db`: đổi tên thủ công nếu cần giữ dữ liệu |

## 4.4 Không có

Không có kho Binary, không có tệp đính kèm lưu bởi auth (`/email/v1/send` đọc `Binary/<id>` từ `fhir-server` bằng token người gọi và **không lưu lại**), không có hàng đợi, không có bộ nhớ đệm bền.
