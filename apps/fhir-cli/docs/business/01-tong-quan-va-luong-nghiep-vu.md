# 1. Tổng quan và luồng nghiệp vụ

## 1.1 Mục tiêu và giá trị

`fhir-cli` cho người vận hành và lập trình viên thao tác với `fhir-server` từ terminal hoặc script mà **không cần viết mã HTTP**: đọc, tìm, ghi, xóa, nạp hàng loạt, xuất hàng loạt và lập lại chỉ mục.

| Giá trị | Cách đạt được |
|---|---|
| Nạp dữ liệu thử và di chuyển dữ liệu nhanh | `import` nhận NDJSON, một resource hoặc Bundle, chia lô, báo lỗi theo từng mục |
| Di chuyển dữ liệu giữa môi trường | `export` rồi `import`; giữ nguyên `id` khi máy chủ đích bật `FHIR_UPDATE_AS_CREATE=true` |
| Tự động hóa | Dữ liệu ra `stdout`, thông báo ra `stderr`, mã thoát ổn định (0, 1, 2), cấu hình bằng biến môi trường |
| An toàn thông tin xác thực | Chỉ đi theo link trên chính máy chủ trong `-base`; không bao giờ gửi token tới host khác |
| Không phụ thuộc | Chỉ thư viện chuẩn của Go, chỉ nói chuyện qua HTTP API của `fhir-server` |

## 1.2 Ranh giới hệ thống

```mermaid
flowchart LR
    subgraph NguoiDung["Con người và tự động hóa"]
        VH["Vận hành<br/>(chuyển dữ liệu, reindex)"]
        DEV["Lập trình viên / QA<br/>(nạp dữ liệu thử, tra cứu)"]
        CI["Script / CI<br/>(đọc mã thoát)"]
    end
    CLI(["<b>fhir-cli</b><br/>một tiến trình, không lưu trạng thái"])
    FHIR["fhir-server<br/>/fhir/R4"]
    AUTH["auth<br/>(cấp token)"]
    FS[("Tệp NDJSON<br/>trên máy chạy CLI")]

    VH & DEV & CI --> CLI
    CLI <-->|"HTTP: Bearer token,<br/>X-Fhir-Project"| FHIR
    AUTH -.->|"token (JWT hoặc token tĩnh)"| CLI
    CLI <--> FS
    FHIR -.->|"kiểm token, project, quyền"| AUTH
```

**Làm:** `read`, `search`, `create`, `update`, `delete`, `import`, `export`, `reindex`, chọn máy chủ và project bằng cờ hoặc biến môi trường.
**Không làm:** websocket subscription, `$validate`, terminology, quản trị project; không tự đăng nhập hay xin token (token do người dùng cấp sẵn); không lưu trạng thái giữa các lần chạy; không ánh xạ lại tham chiếu giữa các resource khi `id` thay đổi; không có image Docker (công cụ dòng lệnh, `make build APP=fhir-cli` ra `bin/fhir-cli`).

## 1.3 Tác nhân

| Mã | Tác nhân | Là ai | Việc chính |
|---|---|---|---|
| A1 | **Vận hành** | SRE / DevOps | Chuyển dữ liệu giữa môi trường (export, import), `reindex` sau khi thêm chỉ mục |
| A2 | **Lập trình viên / QA** | Dev, tester | Nạp dữ liệu thử, tra cứu và sửa tay một resource |
| A3 | **Script / CI** | Tiến trình tự động | Gọi lệnh, quyết định theo mã thoát và `stdout` |
| A4 | **fhir-server** | Máy chủ đích | Thi hành lệnh qua API FHIR; kiểm token và quyền; chạy job nền (export, reindex) |
| A5 | **auth** | Dịch vụ danh tính | Cấp token mà CLI gửi đi (CLI không gọi `auth`) |

## 1.4 Bản đồ năng lực

```mermaid
mindmap
  root((fhir-cli))
    Đọc
      read Type/id
      read theo phiên bản
      search một trang
      search -all -ndjson đi hết các trang
    Ghi
      create
      update
      delete
    Hàng loạt
      import NDJSON Bundle resource đơn
      batch hoặc transaction chia lô
      export system patient group
      lọc type filter since
    Quản trị
      reindex một loại hoặc một resource
    Nền tảng
      Cờ chung và biến môi trường
      Chỉ đi theo link trên chính máy chủ
      Mã thoát 0 1 2
```

## 1.5 Thuật ngữ

| Thuật ngữ | Nghĩa |
|---|---|
| **FHIR R4 / resource** | Chuẩn dữ liệu y tế; một resource là một bản ghi JSON có `resourceType` và `id` |
| **Bundle** | Gói nhiều resource; `batch` (mỗi mục độc lập) hoặc `transaction` (tất cả hoặc không) |
| **NDJSON** | Mỗi dòng một đối tượng JSON; định dạng của `import` và của tệp export |
| **Bulk export (`$export`)** | Job nền của máy chủ gom dữ liệu thành các tệp NDJSON; CLI tạo job, chờ, tải về |
| **`Prefer: respond-async`** | Header yêu cầu máy chủ chạy bất đồng bộ: trả `202` và `Content-Location` là địa chỉ hỏi trạng thái |
| **Update-as-create** | Máy chủ cho phép `PUT Type/id` tạo mới khi `id` chưa có (cấu hình `FHIR_UPDATE_AS_CREATE`) |
| **Reindex (`$reindex`)** | Lập lại chỉ mục tìm kiếm cho một loại hoặc một resource; cần quyền admin |
| **Project** | Tổ chức/không gian dữ liệu; token super-admin chọn project bằng header `X-Fhir-Project` |
| **Lô (chunk)** | Nhóm tối đa `-size` resource (mặc định 100) gửi trong một Bundle |

---

## 1.6 Các luồng nghiệp vụ

```mermaid
flowchart LR
    A["BF-1<br/>Tra cứu và ghi"] --> B["BF-2<br/>Nạp hàng loạt"]
    C["BF-3<br/>Export"] --> D["BF-4<br/>Chuyển dữ liệu<br/>giữa hai máy chủ"]
    B --> D
    E["BF-5<br/>Reindex"] -.-> A
```

### BF-1. Tra cứu và ghi một resource

**Mục tiêu:** xem hoặc sửa nhanh một resource, hoặc lấy ra tất cả resource khớp một tiêu chí.

```mermaid
flowchart TD
    S([Bắt đầu]) --> Q{"Cần gì?"}
    Q -->|"Xem một resource"| R["read Type/id [-version V]<br/>GET Type/id hoặc Type/id/_history/V"]
    Q -->|"Tìm"| SE["search Type 'query'"]
    SE --> P{"Cần mọi trang?"}
    P -->|Không| ONE["In Bundle một trang<br/>(hoặc -ndjson: mỗi resource một dòng)"]
    P -->|Có| ALL["-all -ndjson: đi theo link next<br/>trên chính máy chủ, bỏ resource include"]
    Q -->|"Tạo"| C["create Type -f FILE<br/>POST Type"]
    Q -->|"Sửa"| U["update Type/id -f FILE<br/>PUT Type/id"]
    Q -->|"Xóa"| D["delete Type/id<br/>DELETE Type/id"]
    C --> L["In resource đã tạo (stdout)<br/>và Location (stderr)"]
```
**Kết quả:** JSON ra `stdout`. **Chức năng:** F-01..F-04, F-08, F-09.

### BF-2. Nạp hàng loạt

**Mục tiêu:** đưa nhiều resource vào máy chủ, biết chính xác mục nào lỗi.

```mermaid
sequenceDiagram
    autonumber
    actor V as Người dùng
    participant C as fhir-cli
    participant S as fhir-server
    V->>C: import -f data.ndjson [-mode] [-size]
    C->>C: Đọc toàn bộ đầu vào, nhận dạng: Bundle, resource đơn, hoặc NDJSON
    loop Mỗi lô tối đa size resource
        C->>C: Mỗi resource có id: PUT Type/id; không id: POST Type
        C->>S: POST Bundle (type batch hoặc transaction)
        alt batch
            S-->>C: Bundle phản hồi, mỗi mục một status
            C->>C: Đếm mục 2xx là ok, còn lại là lỗi (in mục N (Type/id): status: diagnostics)
        else transaction và có mục lỗi
            S-->>C: Lỗi cả lô, lô được hoàn tác
            C-->>V: Dừng ngay: gửi Bundle (mục a-b): lỗi. Các lô sau không được gửi
        end
    end
    C-->>V: stdout: đã nạp X, lỗi Y, tổng Z (thoát 1 nếu Y lớn hơn 0)
```

**Kết quả:** `đã nạp X, lỗi Y, tổng Z` ở `stdout`; mã thoát `1` nếu có mục lỗi. **Điều BA cần biết:** (1) `id` có sẵn nhưng chưa tồn tại ở đích sẽ lỗi `404` theo mục nếu đích không bật `FHIR_UPDATE_AS_CREATE`; (2) `transaction` là tất cả hoặc không **theo từng lô**, không theo cả tệp (xem lệch số 1 ở README); (3) tham chiếu giữa các resource **không** được ánh xạ lại khi đích cấp `id` mới. **Chức năng:** F-05, F-08.

### BF-3. Export

**Mục tiêu:** lấy toàn bộ hoặc một phần dữ liệu về tệp NDJSON.

```mermaid
sequenceDiagram
    autonumber
    actor V as Người dùng
    participant C as fhir-cli
    participant S as fhir-server
    V->>C: export -o DIR [-level] [-type] [-filter] [-since] [-keep]
    C->>S: GET $export (hoặc Patient/$export, Group/id/$export) kèm Prefer respond-async
    S-->>C: 202 + Content-Location (địa chỉ job)
    loop Mỗi PollInterval (1 giây), tối đa -wait (30 phút)
        C->>S: GET địa chỉ job
        S-->>C: 202 còn chạy, hoặc 200 + manifest
    end
    loop Mỗi tệp trong manifest
        C->>S: GET url (Accept application/fhir+ndjson)
        C->>C: Ghi DIR/Type-n.ndjson, in đường dẫn và số resource (stdout)
    end
    alt không có -keep
        C->>S: DELETE địa chỉ job (xóa job và tệp trên máy chủ)
    end
    C-->>V: stderr: xong N tệp, M resource
```

**Kết quả:** các tệp `DIR/<Type>-<n>.ndjson`. **Điều BA cần biết:** nếu dọn job thất bại thì chỉ cảnh báo, vẫn thoát 0; nếu lỗi **giữa chừng khi đang tải** thì tệp đã ghi còn nguyên và job **không** bị xóa (còn trên máy chủ tới khi hết `FHIR_EXPORT_RETENTION`, mặc định 24 giờ theo runbook). **Chức năng:** F-06.

### BF-4. Chuyển dữ liệu giữa hai máy chủ

```mermaid
flowchart TD
    A["Máy chủ nguồn"] -->|"fhir-cli export -o dump"| B["dump/Patient-1.ndjson,<br/>dump/Observation-1.ndjson..."]
    B --> C{"Máy chủ đích bật<br/>FHIR_UPDATE_AS_CREATE?"}
    C -->|Có| D["fhir-cli import -f từng tệp<br/>(giữ nguyên id, tham chiếu vẫn khớp,<br/>chạy lại là cập nhật, không lỗi)"]
    C -->|Không| E["Resource có id chưa tồn tại: 404 theo mục<br/>thoát 1; resource không id được cấp id mới<br/>và tham chiếu KHÔNG được ánh xạ lại"]
```
**Thứ tự nạp:** nạp loại được tham chiếu trước (ví dụ `Patient`) rồi loại tham chiếu tới nó (`Observation`); với `FHIR_UPDATE_AS_CREATE` bật, tham chiếu `Patient/<id>` giữ nguyên nên kiểm bằng `search Observation subject=Patient/<id>`. Theo runbook: đây là **xuất logic**, không thay thế sao lưu. **Chức năng:** F-05, F-06.

### BF-5. Reindex

```mermaid
flowchart TD
    S["Thêm chỉ mục tìm kiếm mới"] --> R["reindex Type [id]<br/>POST Type/$reindex hoặc Type/id/$reindex<br/>(cần quyền admin)"]
    R --> A{"Phản hồi có Content-Location?"}
    A -->|Có| W["Chờ job xong (-wait, mặc định 30 phút)<br/>stderr: reindex xong; stdout: kết quả JSON"]
    A -->|Không| Y["Chạy đồng bộ: in kết quả JSON"]
```
**Chức năng:** F-07.

## 1.7 Bảng sự kiện → hiệu lực

| Sự kiện | Hiệu lực |
|---|---|
| Người dùng nhấn Ctrl+C hoặc tiến trình nhận SIGTERM | Yêu cầu HTTP đang chạy bị hủy, lệnh thoát `1`; job export đã tạo **còn trên máy chủ** |
| Token hết hạn hoặc sai | `HTTP 401: ...` ở `stderr`, thoát `1`, không ghi gì |
| Máy chủ đích bật hạn mức | `HTTP 429` theo từng yêu cầu; CLI không tự thử lại |
| Một lô `import` lỗi toàn bộ (transaction) | Dừng; lô đã gửi trước đó vẫn được ghi |
| Tải tệp export quá `-timeout` (60s) | Yêu cầu bị hủy, lệnh thoát `1`, job còn trên máy chủ |
