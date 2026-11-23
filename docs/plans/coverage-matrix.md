# Ma trận phủ kế hoạch: server Medplum so với các kế hoạch hiện có

Trạng thái: **chưa đủ.** (Cập nhật: nhóm mức B đã có kế hoạch chi tiết ở [modules/README.md](modules/README.md); chúng nay là mức A, nhưng còn nhiều câu hỏi mở và hai lỗi bảo mật cần xử lý sớm.) Nguồn: `~/Downloads/medplum/packages/server/src` (v5.2.18). Số dòng là dòng source **không tính test**, do tôi đếm; số dòng chỉ là thước đo thô về quy mô, không phản ánh độ khó. Ba mức: **A** có kế hoạch chi tiết (và có thể đã cài), **B** chỉ có khảo sát ([modules-survey.md](modules-survey.md)), **C** chưa có gì.

## 1. Theo từng khu vực

| Khu vực | Dòng | Mức | Ở đâu / ghi chú |
|---|---|---|---|
| `auth` | 3858 | A | [auth/](auth/README.md); **đã cài** P0, P1, P2, 7.0 (CLI) |
| `oauth` | 4267 | A | `auth` P3, **chưa cài** |
| `admin` | 1926 | A (hoãn) | `auth` P7; CLI 7.0 thay phần tối thiểu |
| `cds` | 82 | A | [cds-dicom/cds.md](cds-dicom/cds.md); chặn bởi quyết định runtime Bot |
| `dicom` (+ worker `dicom`) | 934 (+246) | A | [cds-dicom/dicom.md](cds-dicom/dicom.md) |
| `email` | 548 | A | [email-storage.md](modules/email-storage.md); dùng chung với `Mailer` của `auth` |
| `storage` | 577 | A | [email-storage.md](modules/email-storage.md); là nền của DICOM |
| `keyvalue` | 128 | A | [keyvalue-mcp-webhook.md](modules/keyvalue-mcp-webhook.md); đã có kế hoạch |
| `mcp` | 220 | A | [keyvalue-mcp-webhook.md](modules/keyvalue-mcp-webhook.md); đã có kế hoạch |
| `scim` | 694 | A | [scim-ws-fhircast.md](modules/scim-ws-fhircast.md); thuộc `auth` |
| `webhook` | 74 | A | [keyvalue-mcp-webhook.md](modules/keyvalue-mcp-webhook.md); chặn bởi runtime Bot |
| `ws` | 1965 | A | [scim-ws-fhircast.md](modules/scim-ws-fhircast.md); `subscriptions-r4` đã có ở Go |
| `fhircast` | 930 | A | [scim-ws-fhircast.md](modules/scim-ws-fhircast.md); đã có kế hoạch |
| `fhir` | 37352 | A (kiểm kê) | [modules/fhir-parity-audit.md](modules/fhir-parity-audit.md): 204 mục, 45 CÓ, 35 MỘT PHẦN, 97 CHƯA, 27 KHÔNG ÁP DỤNG; 17 gói công việc |
| `workers` (11 hàng đợi) | 5286 | B/C | chỉ có bảng đối chiếu ở mục 7.3 của khảo sát; chưa có kế hoạch cho `download`, `dispatch`, `set-accounts`, `cron`, `batch` (async) |
| `bots` (`execute`, `vmcontext`, `utils`) | 877 | B/C | chỉ là "phương án D" trong `cds.md`; **chưa có kế hoạch**, mà `cds`, `webhook`, `cron`, `$execute` đều chờ nó |
| `cloud` (aws, azure, gcp, fission) | 2241 | **C** | S3/Azure/GCP storage, SES, Lambda (chạy Bot), Textract, Fission; chưa nhắc tới ngoài việc "chỉ làm SMTP và hệ thống tệp" |
| `ai` (`openai.ts`) | 652 | **C** | gắn với `$ai` và `ws/ai-realtime`; chưa có |
| `config` (loader, SSM, file) | 1293 | C | Go dùng `envconfig` theo từng app; không cần port nguyên, nhưng chưa ghi quyết định |
| `util` | 1795 | C | hàm phụ trợ; đa số sẽ có sẵn hoặc không cần |
| `migrations` | 30248 | C | **migration schema PostgreSQL của Medplum, không áp dụng** cho kho Go (goose riêng). Điều **chưa có** là kế hoạch **chuyển dữ liệu** từ một CSDL Medplum đang chạy (nếu có), xem mục 3 |
| `otel`, `pubsub`, `redis`, `database`, `context`, `logger`, `healthcheck` | 390, 181, 221, 414, 380, 43, 150 | C | hạ tầng; Go đã có phần tương đương (`trivita-observability-go`, `pkg/messaging`, `pkg/database`, `httpserve`) nhưng chưa có bảng đối chiếu |
| `ratelimit`, `heartbeat`, `server-registry` | 197, 36, 147 | C | rate limit là điều kiện trước khi mở `auth` ra Internet (`fhir-server` đã có hạn mức theo IP trong bộ nhớ, bật mặc định 20 rps ở staging/production, + `pkg/httpsec`, 2026-11-21; `auth` cũng đã có, 2026-11-21: chung 20 rps và đăng nhập 1 rps/IP); `server-registry` chưa đọc |
| `seed`, `seeds` | 143, 36 | C | `auth seed project` thay phần cần thiết; `seeds/` nạp search parameter, structure definition, value set đã dựng sẵn |
| `openapi`, `api-catalog`, `wellknown` | 500, 91, 89 | C/A | `wellknown` nằm trong `auth` P3; `openapi.json` và `api-catalog` chưa quyết định |
| `hl7`, `sharding`, `agent` | 26, 18, 14 | C | nhỏ; `hl7` chỉ cần khi có Bot |

Tổng khoảng 69,5 nghìn dòng (chưa tính `migrations`). Phần có kế hoạch chi tiết (mức A) khoảng 11 nghìn dòng, tức khoảng một phần sáu theo dòng, nhưng `fhir` một mình chiếm hơn một nửa nên con số này gây hiểu lầm: theo **số khu vực**, 5 trong khoảng 30 khu vực có kế hoạch chi tiết.

## 2. Ngoài `server`: các package khác của monorepo Medplum

`packages/` còn có `agent`, `app`, `bot-layer`, `ccda`, `cli`, `core`, `fhir-router`, `fhirtypes`, `hl7`, `react`, `react-hooks`, `graphiql`, `generator`, `create-medplum` và các tích hợp (`dosespot-*`, `health-gorilla-*`, `scriptsure-react`). Trong repo Go đã có phần tương đương của `core`/`fhirtypes` (`pkg/fhir`), `fhir-router` (`pkg/fhirrouter`) và `cli` (`apps/fhir-cli`). **`agent`** (cầu nối HL7/DICOM chạy tại cơ sở y tế), `ccda`, `hl7` và các tích hợp bên thứ ba **chưa nằm trong bất kỳ kế hoạch nào**; chưa rõ chúng có thuộc phạm vi migration hay không.

## 3. Quyết định còn thiếu (chặn việc lập kế hoạch cho phần C)

| # | Câu hỏi | Vì sao chặn |
|---|---|---|
| M1 | Có chạy Bot không, và bằng cách nào (HTTP ngoài, sidecar Node, port module Bot)? | Chặn `cds`, `webhook`, `cron`, `Bot/$execute`, `cloud` (Lambda, Fission), `hl7` |
| M2 | Có cần **chuyển dữ liệu** từ CSDL Medplum hiện có sang kho Go không? | Nếu có, cần kế hoạch di trú (ánh xạ bảng, băm mật khẩu `$2b$`, resource, binary); nếu không, `migrations/` hoàn toàn không liên quan |
| M3 | Phạm vi `agent`, `ccda`, `hl7` và các tích hợp bên thứ ba (Health Gorilla, DoseSpot, ScriptSure) | Chưa rõ có thuộc migration không |
| M4 | `ai`: có dùng nhà cung cấp AI nào, và có cần `$ai` và `ws/ai-realtime` không | Chặn `ai` |
| M5 | Lưu trữ đối tượng: chỉ hệ thống tệp, hay S3/Azure/GCP | Chặn `storage` và `cloud` |

## 4. Việc nên làm để "đủ" ở mức kế hoạch

1. Chốt M1 đến M5 (hoặc ghi rõ "ngoài phạm vi").
2. Bản kiểm kê tương đương cho `fhir` (đã ở mục 6 của khảo sát).
3. Viết kế hoạch chi tiết cho các khu vực mức B mà bạn muốn làm trước (đề xuất: `email` + `storage`, `keyvalue`, `mcp`).
4. Kế hoạch cho `workers` (nhóm theo hàng đợi, gắn với từng module sở hữu nó) và cho `bots` nếu chọn phương án D ở M1.
5. Bảng đối chiếu hạ tầng (`otel`, `redis`, `pubsub`, `ratelimit`, `config`, `healthcheck`) để biết phần nào Go đã có, phần nào còn thiếu.
