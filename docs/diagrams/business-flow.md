# Business flow

Sơ đồ Mermaid. Mức chi tiết bám theo mã nguồn `medplum/packages/server/src` (tên hàm, tên file được dẫn nguồn). Phần nào chưa đọc sâu được ghi rõ.

## 1. Bức tranh tổng thể

```mermaid
flowchart LR
  subgraph Clients
    APP[medplum-app / react]
    CLI[medplum-cli]
    EXT[Ứng dụng ngoài<br/>OAuth2 client]
    AG[medplum-agent<br/>HL7, DICOM tại chỗ]
  end

  subgraph Server[medplum-server]
    MW[Middleware<br/>cors, rate limit, request context]
    AUTH[auth, oauth]
    ADM[admin, scim]
    FHIR[fhir-router + fhir<br/>Repository]
    OPS[fhir/operations]
    BOT[bots]
    WS[ws]
    W[workers BullMQ]
  end

  PG[(PostgreSQL)]
  RD[(Redis<br/>cache, queue, pubsub)]
  ST[(Storage<br/>file, S3, Azure, GCS)]
  CL[Cloud<br/>AWS Lambda, Fission]

  APP --> MW
  CLI --> MW
  EXT --> MW
  AG <-->|WebSocket /ws/agent| WS
  MW --> AUTH
  MW --> ADM
  MW --> FHIR
  FHIR --> OPS
  FHIR --> PG
  FHIR --> RD
  FHIR --> ST
  FHIR -->|job nền| RD
  RD --> W
  W --> BOT
  W --> PG
  BOT --> CL
  WS --> RD
```

Thứ tự mount router (`app.ts`): `/.well-known`, `/admin`, `/auth`, `/cds-services`, `/email/v1`, `/fhir/R4`, `/fhircast/STU2|STU3`, `/hub`, `/keyvalue/v1`, `/oauth2`, `/scim/v2`, `/shl`, `/webhook`, `/mcp` (tùy cấu hình).

## 2. Ghi một resource (create, update)

```mermaid
sequenceDiagram
  autonumber
  participant C as Client
  participant R as Express + middleware
  participant F as fhir-router
  participant P as Repository (fhir/repo.ts)
  participant DB as PostgreSQL
  participant Q as Redis (BullMQ)
  participant W as workers

  C->>R: PUT /fhir/R4/Patient/123 + Bearer
  R->>R: rate limit, authenticateRequest
  R->>F: định tuyến interaction "update"
  F->>P: updateResource(resource)
  P->>P: checkResourcePermissions (access policy)
  P->>P: pre-commit (nếu có) và kiểm tra lại quyền
  P->>P: validateResourceReferences
  P->>DB: writeToDatabase trong transaction
  P->>P: setCacheEntry (Redis)
  P->>Q: addBackgroundJobs, addSubscriptionJobs
  P-->>C: 200 + resource (phiên bản mới)
  Q->>W: subscription, download, reindex...
```

Nguồn: `updateResourceImpl`, `writeToDatabase`, `handleStorage` trong `fhir/repo.ts`.

## 3. Subscription kích hoạt bot hoặc thông báo

```mermaid
flowchart TD
  A[Repository ghi resource] --> B[addSubscriptionJobs]
  B --> C{Có Subscription khớp<br/>criteria?}
  C -- không --> Z[Kết thúc]
  C -- có --> D[Đẩy job vào queue subscription]
  D --> E[execSubscriptionJob<br/>workers/subscription.ts]
  E --> F{Kênh}
  F -- endpoint bot --> G[executeBot]
  F -- khác --> H[Gửi tới endpoint đã cấu hình]
  G --> I[Theo dõi lỗi<br/>subscription-failure-tracker]
  H --> I
```

Ghi chú: các kênh chi tiết (rest-hook, websocket, ...) chưa được đọc sâu; nhánh `F` là mức khái quát.

## 4. Thực thi bot

```mermaid
flowchart TD
  A[Bot/$execute, webhook, CDS hook hoặc subscription] --> B[executeBot<br/>bots/execute.ts]
  B --> C{bot.runtimeVersion}
  C -- awslambda --> D[AWS Lambda<br/>cloud/aws/execute.ts]
  C -- vmcontext --> E[Node vm trong tiến trình<br/>bots/vmcontext.ts]
  C -- fission --> F[Fission<br/>cloud/fission/execute.ts]
  C -- khác --> X[Lỗi Unsupported bot runtime]
  D --> R[BotExecutionResult]
  E --> R
  F --> R
```

## 5. Bulk export bất đồng bộ

```mermaid
sequenceDiagram
  participant C as Client
  participant S as Server (operation $export)
  participant Q as Queue bulk-export
  participant W as Worker
  C->>S: POST /fhir/R4/$export (Prefer: respond-async)
  S->>Q: tạo AsyncJob + đưa job vào queue
  S-->>C: 202 + Content-Location (URL trạng thái)
  W->>W: xử lý theo từng resource type
  C->>S: GET /fhir/R4/bulkdata/export/:id
  S-->>C: trạng thái, hoặc danh sách file kết quả khi xong
```

Nguồn: `fhir/operations/export.ts`, `workers/bulk-export.ts`, `fhir/bulkdata.ts`. Mã trạng thái 202 và header `Content-Location` theo chuẩn FHIR Bulk Data; chưa kiểm chứng từng chi tiết trong mã.

## 6. Vòng đời project (tenant)

```mermaid
flowchart LR
  A[Đăng ký user<br/>/auth/newuser] --> B[Tạo project<br/>/auth/newproject]
  B --> C[Project + ProjectMembership admin]
  C --> D[Mời thành viên<br/>/admin/projects/:id/invite]
  C --> E[Tạo client, bot<br/>/admin/projects/:id/client, bot]
  C --> F[Project/$clone, $archive, $restore]
```

## 7. Cụm phụ thuộc cần giữ cùng nhau khi di chuyển

```mermaid
flowchart LR
  fhir <--> operations[fhir/operations]
  fhir --> workers
  workers --> migrations
  migrations --> fhir
  auth <--> oauth
  auth --> fhir
  admin --> auth
  scim --> admin
```

Các mũi tên hai chiều và vòng là lý do `medplum-server` chuyển nguyên khối ở giai đoạn 1.
