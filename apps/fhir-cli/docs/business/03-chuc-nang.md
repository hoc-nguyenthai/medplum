# 3. Danh mục chức năng

Khuôn: **Mục đích · Tác nhân · Lệnh và tham số · Luồng · Đầu ra · Lỗi · Luật**. Thông điệp lỗi nguyên văn từ mã (`internal/cli`, `internal/client`). Luật `BR-xx` ở [04-luat-va-phan-quyen.md](04-luat-va-phan-quyen.md).

| Nhóm | Chức năng |
|---|---|
| **Đọc** | [F-01 read](#f-01) · [F-02 search](#f-02) |
| **Ghi** | [F-03 create, update](#f-03) · [F-04 delete](#f-04) |
| **Hàng loạt** | [F-05 import](#f-05) · [F-06 export](#f-06) |
| **Quản trị** | [F-07 reindex](#f-07) |
| **Nền tảng** | [F-08 cấu hình chung và xác thực](#f-08) · [F-09 chỉ đi theo link trên chính máy chủ](#f-09) · [F-10 mã thoát và hiển thị lỗi](#f-10) |

Các lệnh nhận **tham số vị trí đứng trước hoặc sau cờ** (`read Patient/1 -version 2` và `read -version 2 Patient/1` như nhau); cờ chung (`-base`, `-token`, `-project`, `-timeout`) phải đứng **trước** tên lệnh.

---

<a id="f-01"></a>
### F-01. `read`: đọc một resource

| | |
|---|---|
| **Mục đích** | Xem một resource hoặc một phiên bản của nó |
| **Tác nhân** | A2, A1 |
| **Lệnh** | `fhir-cli read Type/id [-version V]` |
| **Yêu cầu HTTP** | `GET Type/id`, hoặc `GET Type/id/_history/V` khi có `-version` |

**Luồng:** tách `Type/id` (đúng hai thành phần, không có `/` thừa) → gửi `GET` → in JSON thụt 2 khoảng trắng ra `stdout`.
**Lỗi:** `lỗi: cách dùng: read Type/id [-version V]` (số tham số khác 1), `lỗi: cần dạng Type/id, nhận "<x>"` (mã `2`); `lỗi: HTTP <mã>: ...` (mã `1`). **Luật:** BR-01, BR-12.

<a id="f-02"></a>
### F-02. `search`: tìm kiếm

| | |
|---|---|
| **Lệnh** | `fhir-cli search Type [query] [-all] [-ndjson]` |
| **Tham số** | `query` dạng `a=b&c=d` (dấu `?` đầu được bỏ); `-ndjson` in mỗi resource khớp một dòng JSON; `-all` đi hết các trang |
| **Yêu cầu HTTP** | `GET Type?query`, rồi theo link `next` khi có `-all` |

**Luồng:** gửi `GET` → phân tích Bundle → (không `-ndjson`) in nguyên Bundle một trang; (`-ndjson`) in từng `entry.resource` **bỏ qua** resource có `search.mode = include` → có `-all` và có link `next` thì lặp với link đó (chỉ trên chính máy chủ, F-09).
**Đầu ra:** Bundle JSON, hoặc NDJSON.
**Lỗi:** `lỗi: cách dùng: search Type [query] [-all] [-ndjson]` (0 hoặc hơn 2 tham số); `lỗi: -all cần -ndjson (mỗi trang là một Bundle riêng)` (mã `2`, kiểm sau yêu cầu đầu tiên); `lỗi: phản hồi không phải Bundle: ...` (mã `1`); `lỗi: HTTP <mã>: ...`; `lỗi: link "<url>" nằm ngoài <basePath>` (link `next` trỏ ra ngoài). **Luật:** BR-02, BR-03, BR-12.

<a id="f-03"></a>
### F-03. `create`, `update`: ghi một resource

| | |
|---|---|
| **Lệnh** | `fhir-cli create Type -f FILE\|-` ; `fhir-cli update Type/id -f FILE\|-` |
| **Yêu cầu HTTP** | `POST Type` ; `PUT Type/id`, `Content-Type: application/fhir+json` |

**Luồng:** kiểm tham số (`create` nhận *tên loại*, không có `/`; `update` nhận `Type/id`) → đọc `-f` (tệp hoặc `-` là `stdin`) → kiểm là JSON hợp lệ (**chỉ kiểm cú pháp JSON**, không kiểm là resource FHIR hợp lệ: việc đó do máy chủ) → gửi → in resource trả về (`stdout`); nếu có header `Location` thì in `Location: <url>` ra `stderr`.
**Lỗi:** `lỗi: cách dùng: create Type -f FILE | update Type/id -f FILE`; `lỗi: create nhận tên loại resource, ví dụ create Patient -f p.json`; `lỗi: cần dạng Type/id, nhận "<x>"`; `lỗi: cần -f FILE (hoặc -f - để đọc stdin)` (đều mã `2`); `lỗi: nội dung không phải JSON hợp lệ` (mã `1`); `lỗi: HTTP <mã>: ...`. **Luật:** BR-01, BR-04, BR-12.

<a id="f-04"></a>
### F-04. `delete`: xóa một resource

`fhir-cli delete Type/id` → `DELETE Type/id` → `đã xóa Type/id` ra `stderr`, **không in gì ở `stdout`**. Lỗi: `lỗi: cách dùng: delete Type/id`, `lỗi: cần dạng Type/id, nhận "<x>"` (mã `2`); `lỗi: HTTP <mã>: ...`. Không có xác nhận hỏi lại: **xóa ngay**. **Luật:** BR-01, BR-12.

<a id="f-05"></a>
### F-05. `import`: nạp hàng loạt

| | |
|---|---|
| **Mục đích** | Nạp nhiều resource, báo lỗi theo từng mục |
| **Lệnh** | `fhir-cli import -f FILE\|- [-mode batch\|transaction] [-size N]` |
| **Tham số** | `-f` bắt buộc; `-mode` mặc định `batch`; `-size` mặc định `100`, tối thiểu `1` |
| **Đầu vào** | NDJSON (mỗi dòng một resource; dòng trống bỏ qua), **hoặc** một resource đơn, **hoặc** một Bundle (lấy `entry[].resource`, **bỏ qua** `Bundle.type` và `entry.request` của đầu vào) |

**Luồng:** đọc **toàn bộ** đầu vào vào bộ nhớ → nhận dạng (một giá trị JSON duy nhất có `resourceType`: Bundle hoặc resource đơn; ngược lại coi là NDJSON) → với mỗi resource: có `id` thì `PUT Type/id`, không có `id` thì `POST Type` → chia lô `-size` → mỗi lô gửi một Bundle `batch` hoặc `transaction` tới gốc API → đọc Bundle phản hồi, kiểm số mục khớp → mục `status` bắt đầu bằng `2` là thành công, còn lại là lỗi.
**Đầu ra:** `đã nạp X, lỗi Y, tổng Z` (`stdout`); mỗi mục lỗi `mục N (Type[/id]): <status>[: <diagnostics>]` (`stderr`, `N` là số dòng NDJSON hoặc vị trí trong Bundle, tính từ 1). Mã thoát `1` kèm `lỗi: K resource không nạp được` nếu `Y > 0`.
**Lỗi:** `lỗi: cách dùng: import -f FILE|- [-mode batch|transaction] [-size N]` (mã `2`: có tham số vị trí, `-mode` lạ, `-size` < 1); `lỗi: cần -f FILE ...`; `lỗi: đầu vào rỗng`; `lỗi: đầu vào phải là resource hoặc Bundle có resourceType`; `lỗi: dòng N không phải JSON hợp lệ`; `lỗi: entry N của Bundle không có resource`; `lỗi: mục N thiếu resourceType`; `lỗi: gửi Bundle (mục a-b): <lỗi máy chủ>` (cả lô lỗi, thường ở `transaction`); `lỗi: phản hồi Bundle không khớp số mục gửi (N)`.
**Luật:** BR-05..BR-09, BR-13, BR-15.

<a id="f-06"></a>
### F-06. `export`: bulk export và tải về

| | |
|---|---|
| **Mục đích** | Lấy dữ liệu về tệp NDJSON |
| **Lệnh** | `fhir-cli export -o DIR [-level system\|patient\|group/ID] [-type A,B] [-filter Type?query]... [-since T] [-keep] [-wait D]` |
| **Tham số** | `-o` bắt buộc; `-level` mặc định `system`; `-type` ghép thành `_type`; `-filter` lặp lại được, mỗi giá trị thành một `_typeFilter`; `-since` thành `_since` (CLI **không kiểm** định dạng, máy chủ kiểm); `-keep` giữ job; `-wait` mặc định 30 phút |
| **Yêu cầu HTTP** | `GET $export` \| `Patient/$export` \| `Group/<id>/$export` kèm `Prefer: respond-async` |

**Luồng:** tạo job (cần `202` kèm `Content-Location`) → hỏi trạng thái mỗi giây (`202` còn chạy) tới khi xong hoặc hết `-wait` → đọc manifest `output[]` → với mỗi tệp: kiểm tên loại an toàn, tải (`Accept: application/fhir+ndjson`), ghi `DIR/<Type>-<n>.ndjson` (`n` đếm theo loại, từ 1), in `<đường dẫn>\t<count> resource` (`stdout`) → in `xong: N file, M resource` (`stderr`) → nếu không có `-keep`: `DELETE` job (thất bại chỉ cảnh báo).
**Lỗi:** `lỗi: cách dùng: export -o DIR [...]` (thiếu `-o` hoặc có tham số vị trí); `lỗi: -level phải là system, patient hoặc group/ID` (mã `2`); `lỗi: máy chủ không bắt đầu job export (HTTP <mã>)`; `lỗi: hết thời gian chờ job (<D>)`; `lỗi: manifest không hợp lệ: ...`; `lỗi: manifest có tên loại không hợp lệ: "<x>"`; `lỗi: tải <url>: <lỗi>`; `cảnh báo: không dọn được job trên máy chủ: <lỗi>` (mã vẫn `0`).
**Luật:** BR-10, BR-11, BR-13..BR-16.

<a id="f-07"></a>
### F-07. `reindex`: lập lại chỉ mục

`fhir-cli reindex Type [id] [-wait D]` → `POST Type/$reindex` hoặc `POST Type/<id>/$reindex` (body rỗng). Có `Content-Location` thì chờ job (như export) rồi in `reindex xong` (`stderr`) và kết quả JSON (`stdout`); không có thì coi là chạy đồng bộ và in thẳng phản hồi. **Lỗi:** `lỗi: cách dùng: reindex Type [id]` (mã `2`; tham số đầu có `/`, nghĩa là `reindex Patient/1` bị từ chối); `lỗi: HTTP 403: ...` khi thiếu quyền admin; `lỗi: hết thời gian chờ job (<D>)`. **Luật:** BR-12, BR-16, BR-17.

<a id="f-08"></a>
### F-08. Cấu hình chung và xác thực

| Cờ | Biến môi trường | Mặc định | Ý nghĩa |
|---|---|---|---|
| `-base` | `FHIR_BASE_URL` | `http://localhost:8005/fhir/R4` | Gốc API; phải là `http` hoặc `https` có host |
| `-token` | `FHIR_TOKEN` | (rỗng: không gửi) | Gửi `Authorization: Bearer <token>` |
| `-project` | `FHIR_PROJECT` | (rỗng: không gửi) | Gửi `X-Fhir-Project: <id>` (token super-admin) |
| `-timeout` | không có | `60s` | Hạn **mỗi yêu cầu HTTP**, kể cả tải tệp |

Mọi yêu cầu gửi `Accept: application/fhir+json`, và `Content-Type: application/fhir+json` khi có body. Cờ thắng biến môi trường. `help` hoặc không đối số in trợ giúp (`help` thoát `0`, không đối số thoát `2`). **Lỗi:** `lỗi: URL gốc không hợp lệ: "<x>" (cần dạng http://host:port/fhir/R4)` (mã `2`); cờ lạ in thông báo của Go và thoát `2`. **Luật:** BR-18, BR-19.

<a id="f-09"></a>
### F-09. Chỉ đi theo link trên chính máy chủ

Link phân trang (`search -all`), `Content-Location` của job và URL tệp export có thể là địa chỉ tuyệt đối do máy chủ khai báo (khác địa chỉ client dùng, ví dụ sau proxy). CLI **chỉ lấy path và query** của link rồi ghép vào `-base`, và **từ chối** link có path nằm ngoài path của `-base`: `link "<url>" nằm ngoài <basePath>`. Nhờ đó **không bao giờ gửi token tới host khác** (đã có test đếm yêu cầu thiếu token). **Luật:** BR-02, BR-20.

<a id="f-10"></a>
### F-10. Mã thoát và hiển thị lỗi

Mã `0` thành công, `1` lỗi, `2` sai cách dùng (chi tiết ở [02-luong-nguoi-dung.md mục 2.2](02-luong-nguoi-dung.md)). Lỗi từ máy chủ hiển thị `lỗi: HTTP <mã>: <thông điệp>`, trong đó thông điệp lấy từ `OperationOutcome` (`diagnostics`, nếu rỗng thì `details.text`; nhiều issue nối bằng `; `) hoặc từ JSON `{"error": ...}`, nếu không thì nguyên văn thân phản hồi (dưới 500 ký tự), nếu không thì dòng trạng thái HTTP. Hủy bằng Ctrl+C hoặc SIGTERM hủy yêu cầu đang chạy và thoát `1`. **Luật:** BR-21.
