#!/usr/bin/env python3
"""Sinh docs/features/*.md (nội dung viết tay, chỉ nêu điều đã xác minh trong mã)."""
import os
OUT = '/Users/hocnt84/Documents/TRIVITAAI/trivita-codebase-v1/docs/features'
os.makedirs(OUT, exist_ok=True)
S = lambda m: f'[`server/{m}`](../modules/server/{m.replace("/", "-").strip("()")}.md)'
P = lambda p: f'[`{p}`](../modules/packages/{p}.md)'

F = {}
F['fhir-data-api'] = dict(
 title='API dữ liệu FHIR (CRUD, search, history, batch, GraphQL)',
 purpose='Lưu và truy xuất resource FHIR R4 theo REST chuẩn: tạo, đọc, sửa, vá, xóa, lịch sử phiên bản, tìm kiếm, batch/transaction, GraphQL.',
 mods=[S('fhir'), S('migrations'), S('(root)'), P('fhir-router'), P('core'), P('definitions'), P('fhirtypes')],
 api=['[fhir-rest.md](../api-contract/fhir-rest.md)'],
 notes=['`fhir-router` tách phần định tuyến REST khỏi lưu trữ; server cài `Repository` trên Postgres, mock cài trên bộ nhớ.',
        'Repository (`server/src/fhir/repo.ts`) kiểm tra quyền, chạy pre-commit, kiểm tra tham chiếu, ghi DB trong transaction, cập nhật cache Redis, rồi đưa job nền vào hàng đợi.',
        'Bảng tra cứu (lookup tables) và cột token phục vụ search nằm trong `fhir/lookups` và `fhir/token-column.ts`.',
        'Điểm khó khi chuyển: `fhir` gọi `fhir/operations` 86 lần và ngược lại 96 lần (vòng phụ thuộc), nên hai module phải chuyển cùng nhau.'])
F['authentication'] = dict(
 title='Xác thực và phiên đăng nhập (auth, MFA, OAuth2/OIDC)',
 purpose='Đăng ký user và project, đăng nhập mật khẩu hoặc Google, MFA (TOTP, email), chọn profile, đặt lại mật khẩu, cấp token OAuth2/OIDC.',
 mods=[S('auth'), S('oauth'), S('email'), S('config')],
 api=['[auth.md](../api-contract/auth.md)', '[oauth.md](../api-contract/oauth.md)'],
 notes=['`/oauth2/token` hỗ trợ 5 grant: `authorization_code`, `refresh_token`, `client_credentials`, token-exchange, `pre-authorized_code`.',
        'PKCE qua `codeChallenge` khi đăng nhập và `code_verifier` khi đổi token.',
        'Login/membership/user là resource FHIR (`Login`, `ProjectMembership`, `User`) lưu qua `fhir`; vì vậy `auth` phụ thuộc `fhir` 48 lần.',
        'Trivita đã có `authz-service` và skill `identity-service-go`. Cần quyết định giữ nguyên module này hay thay bằng service Go (ảnh hưởng đến toàn bộ giai đoạn 2).'])
F['project-and-access'] = dict(
 title='Project, thành viên và phân quyền',
 purpose='Mỗi project là một tenant. Quản lý thành viên, mời user, client application, bot, secret, access policy theo resource.',
 mods=[S('admin'), S('fhir'), S('scim'), S('auth')],
 api=['[admin.md](../api-contract/admin.md)', '[others.md](../api-contract/others.md) (SCIM)'],
 notes=['Access policy xử lý trong `fhir/accesspolicy.ts` và `default-access-policies.ts`.',
        'Operation vòng đời project: `Project/$init`, `$clone`, `$archive`, `$restore`, `$rate-limits`.',
        'Rate limit tính theo project (cấu hình `defaultLoginRateLimit`, `defaultMfaRateLimit`, ... trong `ratelimit.ts`).',
        'SCIM v2 dùng cho provisioning user từ IdP bên ngoài.'])
F['bots-automation'] = dict(
 title='Bot và tự động hóa (bot, CDS Hooks, webhook)',
 purpose='Chạy mã tùy biến của khách hàng phản ứng với sự kiện hoặc gọi trực tiếp qua `$execute`.',
 mods=[S('bots'), S('cloud'), S('cds'), S('webhook'), P('bot-layer')],
 api=['[fhir-rest.md](../api-contract/fhir-rest.md) (`Bot/$execute`)', '[fhir-operations.md](../api-contract/fhir-operations.md) (`Bot/$init`, `Bot/:id/$deploy`)', '[others.md](../api-contract/others.md) (CDS, webhook)'],
 notes=['`executeBot` chọn runtime theo `bot.runtimeVersion`: `awslambda`, `vmcontext` hoặc `fission`; giá trị khác trả "Unsupported bot runtime".',
        'Triển khai bot: `cloud/aws/deploy.ts` (Lambda) hoặc `cloud/fission/deploy.ts`.',
        'CDS Hooks và webhook đều thực thi qua bot.',
        '`bot-layer` đóng gói thư viện (core, ccda, definitions) thành Lambda layer.'])
F['subscriptions-events'] = dict(
 title='Subscription và job nền',
 purpose='Phát hiện thay đổi resource và kích hoạt thông báo hoặc bot; xử lý tác vụ nặng bất đồng bộ bằng BullMQ.',
 mods=[S('workers'), S('ws'), S('fhir'), S('otel')],
 api=['[others.md](../api-contract/others.md) (WebSocket `/ws/subscriptions-r4`)'],
 notes=['Sau khi ghi resource, `repo.ts` gọi `addBackgroundJobs` và `addSubscriptionJobs`; job chạy ở `workers/subscription.ts` (`execSubscriptionJob`).',
        'Các worker hiện có: `subscription`, `download`, `reindex`, `batch`, `bulk-export`, `dispatch`, `cron`, `set-accounts`, `dicom`, `lambda-cleaner`, `post-deploy-migration`, `subscription-failure-tracker`.',
        'Điểm khó: `workers → migrations → fhir → workers` tạo vòng; phải tách trước khi có `medplum-worker`.'])
F['bulk-data-jobs'] = dict(
 title='Bulk data và AsyncJob',
 purpose='Xuất dữ liệu lớn (`$export` ở mức hệ thống, Group, Patient) và các tác vụ chạy lâu, theo dõi qua AsyncJob.',
 mods=[S('workers'), S('fhir'), S('fhir/operations')],
 api=['[fhir-operations.md](../api-contract/fhir-operations.md) (`$export`)', '[fhir-rest.md](../api-contract/fhir-rest.md) (`/bulkdata/export/:id`, `/job/:id/status`)'],
 notes=['Worker `bulk-export` xử lý job; trạng thái và kết quả theo dõi qua resource `AsyncJob`/`BulkDataExport` và route `bulkdata/export/:id`.',
        '`AsyncJob/:id/$cancel` hủy job.'])
F['terminology'] = dict(
 title='Terminology (ValueSet, CodeSystem, ConceptMap)',
 purpose='Mở rộng ValueSet, tra cứu và xác thực mã, dịch ConceptMap, nhập CodeSystem và ConceptMap.',
 mods=[S('fhir/operations'), S('seeds'), P('definitions')],
 api=['[fhir-operations.md](../api-contract/fhir-operations.md) (`$expand`, `$lookup`, `$validate-code`, `$subsumes`, `$translate`, `$import`)'],
 notes=['`seeds` nạp ValueSet, StructureDefinition, SearchParameter ban đầu; super admin có route nạp lại.'])
F['clinical-operations'] = dict(
 title='Nghiệp vụ lâm sàng (everything, summary, C-CDA, lịch hẹn, SMART Health)',
 purpose='Các operation gắn với nghiệp vụ lâm sàng và vận hành phòng khám.',
 mods=[S('fhir/operations'), P('ccda'), P('react-scheduling')],
 api=['[fhir-operations.md](../api-contract/fhir-operations.md)'],
 notes=['`Patient/$everything|$summary|$match|$ccda-export`, `QuestionnaireResponse/$extract`, `Measure/$evaluate-measure`, `PlanDefinition/$apply`, `ChargeItemDefinition/$apply`.',
        'Lịch hẹn: `Appointment/$find`, `$book`, `$hold`, `$cancel`, `$confirm`, `$reschedule`.',
        'Thanh toán: `Claim/$submit|$export`, `CoverageEligibilityRequest/$submit`.',
        'SMART Health Card và Link: `$generate-smart-health-card`, `$verify-smart-health-card`, `$generate-smart-health-link`, `$resolve-smart-health-link`.'])
F['integrations-edge'] = dict(
 title='Tích hợp thiết bị và hệ thống ngoài (Agent, HL7, DICOM, FHIRcast, MCP)',
 purpose='Kết nối hệ thống tại chỗ (HL7 v2, DICOM), đồng bộ ngữ cảnh lâm sàng và công cụ AI.',
 mods=[P('agent'), P('hl7'), S('dicom'), S('fhircast'), S('mcp'), S('ai'), S('ws'), S('agent')],
 api=['[others.md](../api-contract/others.md)', '[fhir-operations.md](../api-contract/fhir-operations.md) (`Agent/$*`)'],
 notes=['Agent kết nối server qua WebSocket `/ws/agent`; server điều khiển bằng `Agent/$status|$stats|$upgrade|$fetch-logs|$reload-config|$push`.',
        'DICOMweb mới cài `STOW-RS`, `QIDO-RS` (study, series) và metadata series; các route còn lại là stub.',
        'FHIRcast hỗ trợ STU2 và STU3 (cả `/hub/`).',
        'MCP phục vụ công cụ AI qua `/mcp/stream` và `/mcp/sse` (bật theo cấu hình).'])
F['storage-binary'] = dict(
 title='Lưu trữ file (Binary)',
 purpose='Lưu nội dung nhị phân trên filesystem, S3, Azure Blob hoặc Google Cloud Storage; presigned URL; quét Binary.',
 mods=[S('storage'), S('cloud'), S('fhir')],
 api=['[fhir-operations.md](../api-contract/fhir-operations.md) (`Binary/:id/$presigned-url`, `$scan`)'],
 notes=['Backend chọn qua cấu hình `binaryStorage` (`file:`, `s3:`, ...).'])
F['platform-ops'] = dict(
 title='Vận hành nền tảng (super admin, DB, observability)',
 purpose='Công cụ cho vận hành: migration, reindex, vacuum, kiểm tra index, thống kê, telemetry.',
 mods=[S('admin'), S('migrations'), S('otel'), S('sharding'), S('(root)'), P('cdk')],
 api=['[admin.md](../api-contract/admin.md) (`/admin/super/*`)', '[fhir-operations.md](../api-contract/fhir-operations.md) (`$db-*`, `$explain`)'],
 notes=['Healthcheck và heartbeat trong `server/src/healthcheck.ts`, `heartbeat.ts`.',
        'Hạ tầng AWS bằng CDK (`medplum-cdk`); Helm chart và Terraform nằm ở `medplum/charts`, `medplum/terraform`, chuyển sang `deploy/medplum/`.'])
F['frontend'] = dict(
 title='Frontend (admin app, thư viện React, tích hợp kê đơn và xét nghiệm)',
 purpose='Giao diện quản trị và thư viện dùng lại để dựng ứng dụng lâm sàng.',
 mods=[P('app'), P('react'), P('react-hooks'), P('react-scheduling'), P('dosespot-react'), P('health-gorilla-react'), P('scriptsure-react'), P('graphiql')],
 api=['Gọi toàn bộ API ở [api-contract](../api-contract/README.md) qua `MedplumClient` trong `@medplum/core`.'],
 notes=['Chỉ phụ thuộc `core`, `fhirtypes`, `definitions`; không phụ thuộc `server`.',
        'Chuyển song song với backend; không có vòng phụ thuộc.'])

idx = ['# Tính năng\n', '| Tính năng | Module chính |', '|---|---|']
for k, f in F.items():
    body = f"""# {f['title']}

{f['purpose']}

## Module liên quan

{chr(10).join('- ' + m for m in f['mods'])}

## API contract

{chr(10).join('- ' + a for a in f['api'])}

## Ghi chú kỹ thuật và di chuyển

{chr(10).join('- ' + n for n in f['notes'])}

Sơ đồ liên quan: [business flow](../diagrams/business-flow.md), [user flow](../diagrams/user-flow.md).
"""
    open(f'{OUT}/{k}.md', 'w').write(body)
    idx.append(f"| [{f['title']}]({k}.md) | {', '.join(f['mods'][:3])} |")
open(f'{OUT}/README.md', 'w').write('\n'.join(idx) + '\n')
print(len(F), 'features')
