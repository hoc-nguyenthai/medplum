#!/usr/bin/env python3
"""Sinh docs/{modules,api-contract} từ mã nguồn medplum/ (chỉ đọc)."""
import json, os, re, collections, sys

ROOT = '/Users/hocnt84/Documents/TRIVITAAI/trivita-codebase-v1'
MP = f'{ROOT}/medplum/packages'
SRV = f'{MP}/server/src'
OUT = f'{ROOT}/docs'

# ---------------------------------------------------------------- packages
# tên thư mục -> (đích, loại, mô tả)
PKG = {
 'fhirtypes': ('libs/medplum-fhirtypes', 'lib', 'Kiểu TypeScript (.d.ts) cho toàn bộ resource FHIR R4. Không có logic chạy.'),
 'definitions': ('libs/medplum-definitions', 'lib', 'Dữ liệu định nghĩa FHIR dạng JSON (StructureDefinition, SearchParameter, ValueSet) để core, server và mock nạp.'),
 'core': ('libs/medplum-core', 'lib', 'Thư viện lõi: MedplumClient, FHIRPath, parser và validator kiểu FHIR, search parameter, tiện ích chung. Gần như mọi package khác phụ thuộc vào đây.'),
 'hl7': ('libs/medplum-hl7', 'lib', 'Parse và serialize HL7 v2, client/server MLLP.'),
 'ccda': ('libs/medplum-ccda', 'lib', 'Chuyển đổi hai chiều giữa C-CDA và FHIR.'),
 'fhir-router': ('libs/medplum-fhir-router', 'lib', 'Router FHIR độc lập framework: ánh xạ REST (read, search, create, update, patch, delete, history, batch, GraphQL) sang interface Repository. Server và mock cùng dùng.'),
 'mock': ('libs/medplum-mock', 'lib', 'MockClient và repository in-memory dùng cho test, storybook, ví dụ. Chạy trên fhir-router.'),
 'react-hooks': ('libs/medplum-react-hooks', 'lib', 'Hook React (useMedplum, useSearch, useResource, ...) và MedplumProvider.'),
 'react': ('libs/medplum-react', 'lib', 'Thư viện component UI (form, bảng, resource viewer, timeline, ...), nhóm lớn nhất frontend.'),
 'react-scheduling': ('libs/medplum-react-scheduling', 'lib', 'Component lập lịch hẹn (chọn slot, đặt lịch) dựa trên $find, $book.'),
 'dosespot-core': ('libs/medplum-dosespot-core', 'lib', 'SDK tích hợp DoseSpot (kê đơn điện tử): client, kiểu, hằng số.'),
 'dosespot-react': ('libs/medplum-dosespot-react', 'lib', 'Component React cho DoseSpot.'),
 'health-gorilla-core': ('libs/medplum-health-gorilla-core', 'lib', 'SDK tích hợp Health Gorilla (xét nghiệm, hình ảnh): client, kiểu.'),
 'health-gorilla-react': ('libs/medplum-health-gorilla-react', 'lib', 'Component React cho Health Gorilla.'),
 'scriptsure-react': ('libs/medplum-scriptsure-react', 'lib', 'Component React tích hợp ScriptSure (kê đơn điện tử).'),
 'server': ('apps/medplum-server', 'app', 'Máy chủ FHIR (Express + PostgreSQL + Redis): REST FHIR R4, OAuth2/OIDC, quản trị project, bot, subscription, bulk export, job nền. Khối lớn nhất (791 file).'),
 'bot-layer': ('apps/medplum-bot-layer', 'app', 'Lambda layer chứa thư viện cho bot chạy trên AWS Lambda (đóng gói core, ccda, definitions).'),
 'agent': ('apps/medplum-agent', 'app', 'Agent cài tại chỗ: cầu nối HL7 v2, DICOM, thiết bị với Medplum qua WebSocket. Có bản cài Windows, Linux, macOS.'),
 'app': ('apps/medplum-app', 'app', 'Giao diện quản trị web (Vite + React) cho Medplum.'),
 'cli': ('apps/medplum-cli', 'app', 'Công cụ dòng lệnh `medplum` (đăng nhập, deploy bot, bulk, project).'),
 'cli-wrapper': ('apps/medplum-cli-wrapper', 'app', 'Vỏ mỏng của CLI, phát hành với tên package `medplum`.'),
 'cdk': ('apps/medplum-cdk', 'app', 'Hạ tầng AWS bằng CDK (VPC, RDS, ECS, S3, CloudFront).'),
 'generator': ('tools/medplum-generator', 'tool', 'Sinh mã: fhirtypes, definitions, search parameter từ đặc tả FHIR.'),
 'e2e': ('tools/medplum-e2e', 'tool', 'Test end-to-end (Playwright).'),
 'storybook': ('tools/medplum-storybook', 'tool', 'Storybook cho component react.'),
 'graphiql': ('tools/medplum-graphiql', 'tool', 'GraphiQL trỏ tới endpoint GraphQL của server.'),
 'docs': ('tools/medplum-docs', 'tool', 'Trang tài liệu Docusaurus (nội dung của Medplum).'),
 'examples': ('tools/medplum-examples', 'tool', 'Mã ví dụ dùng trong tài liệu, có test.'),
 'eslint-config': ('tools/medplum-eslint-config', 'tool', 'Cấu hình ESLint dùng chung.'),
 'create-medplum': ('tools/medplum-create-medplum', 'tool', 'Trình khởi tạo `npm create medplum`.'),
}
BATCH = {'fhirtypes':'A','definitions':'A','core':'A','hl7':'B','ccda':'B','fhir-router':'B','mock':'B',
         'react-hooks':'C','react':'C','react-scheduling':'C','dosespot-core':'C','dosespot-react':'C',
         'health-gorilla-core':'C','health-gorilla-react':'C','scriptsure-react':'C'}
APP_ORDER = ['bot-layer','server','agent','cli','cli-wrapper','cdk','app']

def count_ts(d):
    n = 0
    for r, ds, fs in os.walk(d):
        ds[:] = [x for x in ds if x not in ('node_modules', 'dist', 'build', 'coverage')]
        n += sum(1 for f in fs if f.endswith(('.ts', '.tsx')) and not f.endswith(('.test.ts', '.test.tsx', '.stories.tsx')))
    return n

def short(n): return n.replace('@medplum/', '')

def gen_packages():
    meta = {}
    for d in PKG:
        j = json.load(open(f'{MP}/{d}/package.json'))
        meta[d] = j
    byname = {j['name']: d for d, j in meta.items()}
    rdeps = collections.defaultdict(set)
    info = {}
    for d, j in meta.items():
        rt = sorted({byname[k] for s in ('dependencies', 'peerDependencies') for k in j.get(s, {}) if k in byname})
        dv = sorted({byname[k] for k in j.get('devDependencies', {}) if k in byname} - set(rt))
        info[d] = (rt, dv)
        for x in rt: rdeps[x].add(d)
    os.makedirs(f'{OUT}/modules/packages', exist_ok=True)
    rows = []
    for d, (dest, kind, desc) in PKG.items():
        j = meta[d]; rt, dv = info[d]
        srcdirs = sorted(x for x in os.listdir(f'{MP}/{d}/src')) if os.path.isdir(f'{MP}/{d}/src') else []
        scripts = ', '.join(f'`{k}`' for k in j.get('scripts', {}) if k in ('build', 'dev', 'start', 'test', 'lint'))
        bin_ = j.get('bin'); bin_ = list(bin_) if isinstance(bin_, dict) else ([bin_] if bin_ else [])
        n = count_ts(f'{MP}/{d}')
        if kind == 'lib': step = f'Bước 3, đợt {BATCH[d]}'
        elif kind == 'app': step = f'Bước 4, thứ tự {APP_ORDER.index(d)+1}/{len(APP_ORDER)}'
        else: step = 'Bước 4, sau cùng (không phải domain)'
        lk = lambda xs: ', '.join(f'[`{x}`]({x}.md)' for x in xs) or '—'
        body = f"""# {j['name']}

> {desc}

| | |
|---|---|
| Loại | {kind} |
| Đường dẫn hiện tại | `medplum/packages/{d}` |
| Đường dẫn đích | `{dest}` |
| Số file nguồn (không tính test) | {n} |
| Thư mục `src/` | {', '.join(f'`{x}`' for x in srcdirs) or '—'} |
| Entry point | `{j.get('main') or '—'}` |
| Lệnh CLI | {', '.join(f'`{b}`' for b in bin_) or '—'} |
| Script npm | {scripts or '—'} |
| Thứ tự di chuyển | {step} |

## Phụ thuộc

- Runtime: {lk(rt)}
- Chỉ dev: {lk(dv)}
- Được dùng bởi: {lk(sorted(rdeps[d]))}

## Ghi chú di chuyển

- Giữ nguyên tên package `{j['name']}`; chỉ đổi thư mục. Import trong mã không đổi.
- Thư mục đích sâu 2 cấp nên `extends "../../tsconfig.json"` vẫn đúng.
- Sau khi chuyển: `turbo run build test --filter={j['name']}` phải xanh.
"""
        open(f'{OUT}/modules/packages/{d}.md', 'w').write(body)
        rows.append((d, j['name'], kind, dest, n, rt))
    return rows

# ---------------------------------------------------------------- server modules
# module -> (mô tả, cụm)
SM = {
 '(root)': ('Khởi tạo ứng dụng: app.ts (middleware, mount router), database.ts, redis.ts, ratelimit, healthcheck, context, openapi, pubsub, logger.', 'lõi'),
 'config': ('Nạp cấu hình (file JSON, biến môi trường, AWS SSM, GCP, Azure secret) thành MedplumServerConfig.', 'lõi'),
 'util': ('Hàm tiện ích: ID, hash, mật khẩu, HTTP, ngày giờ, chuỗi.', 'lõi'),
 'fhir': ('Trung tâm: Repository (CRUD, search, history), access policy, lookup tables, token column, batch, binary, bulk data, projectmembership, SMART.', 'lõi'),
 'fhir/operations': ('Các FHIR operation `$...` (113 route): export, expand, translate, $everything, $summary, Appointment, Agent, DB admin, Project clone/archive.', 'lõi'),
 'migrations': ('Schema Postgres và migration (phần lớn sinh tự động).', 'lõi'),
 'workers': ('Job nền BullMQ: subscription, download, reindex, batch, bulk-export, cron, dispatch, set-accounts.', 'lõi'),
 'cloud': ('Adapter cloud: AWS (Lambda, S3, SES, Textract), Azure, GCP, Fission.', 'lõi'),
 'storage': ('Lưu file Binary: filesystem, S3, Azure Blob, GCS; presigned URL.', 'lõi'),
 'bots': ('Chạy bot: vmcontext, AWS Lambda, Fission.', 'lõi'),
 'otel': ('OpenTelemetry: trace, metric.', 'lõi'),
 'auth': ('Đăng ký, đăng nhập, MFA, đổi/đặt lại mật khẩu, Google, token exchange, profile, scope.', 'danh tính'),
 'oauth': ('OAuth2/OIDC: authorize, token (5 grant), userinfo, introspect, logout, register, JWKS.', 'danh tính'),
 'admin': ('Quản trị project (mời user, tạo bot/client, member, secret, site) và super admin (reindex, purge, vacuum, migrate).', 'danh tính'),
 'scim': ('SCIM v2: provisioning user.', 'danh tính'),
 'email': ('Gửi email (SES hoặc SMTP).', 'phụ'),
 'dicom': ('DICOMweb: QIDO-RS, STOW-RS, WADO-RS.', 'phụ'),
 'fhircast': ('FHIRcast STU2/STU3: đồng bộ ngữ cảnh giữa ứng dụng lâm sàng.', 'phụ'),
 'ws': ('WebSocket: subscription, agent, fhircast, AI realtime, echo.', 'phụ'),
 'mcp': ('Máy chủ MCP cho công cụ AI (stream và SSE).', 'phụ'),
 'ai': ('Tích hợp OpenAI cho operation `$ai`.', 'phụ'),
 'agent': ('Hàm hỗ trợ giao tiếp server với Agent.', 'phụ'),
 'keyvalue': ('API key-value trên Redis.', 'phụ'),
 'cds': ('CDS Hooks: liệt kê dịch vụ, thực thi bot làm CDS service.', 'phụ'),
 'webhook': ('Nhận webhook đến, chạy bot tương ứng.', 'phụ'),
 'sharding': ('Chọn shard DB theo project.', 'phụ'),
 'seeds': ('Dữ liệu khởi tạo: ValueSet, StructureDefinition, SearchParameter.', 'lõi'),
 'hl7': ('Parser HL7 phía server.', 'phụ'),
}

def modname(p):
    parts = p.split('/')
    if len(parts) == 1: return '(root)'
    if parts[0] == 'fhir' and len(parts) > 2 and parts[1] == 'operations': return 'fhir/operations'
    return parts[0]

def graph():
    imp = re.compile(r"""(?:from|import)\s*\(?\s*['"](\.[^'"]*)['"]""")
    g = collections.defaultdict(collections.Counter); files = collections.defaultdict(list)
    for r, _, fs in os.walk(SRV):
        for f in fs:
            if not f.endswith('.ts') or f.endswith('.test.ts') or '__mocks__' in r or '@types' in r: continue
            p = os.path.relpath(os.path.join(r, f), SRV); m = modname(p); files[m].append(p)
            for i in imp.findall(open(os.path.join(r, f), errors='ignore').read()):
                t = os.path.normpath(os.path.join(os.path.dirname(p), i))
                if t.startswith('..'): continue
                d = t if (os.path.isdir(f'{SRV}/{t}') and '/' not in t) else (t if '/' in t else '(root)')
                dst = modname(d if '/' in d else (d if os.path.isdir(f'{SRV}/{d}') else d + '.ts'))
                if dst != m: g[m][dst] += 1
    return g, files

MOUNT = {'authRouter': '/auth', 'oauthRouter': '/oauth2', 'projectAdminRouter': '/admin/projects',
 'superAdminRouter': '/admin/super', 'cdsRouter': '/cds-services', 'emailRouter': '/email/v1',
 'keyValueRouter': '/keyvalue/v1', 'scimRouter': '/scim/v2', 'webhookRouter': '/webhook',
 'mcpRouter': '/mcp', 'wellKnownRouter': '/.well-known', 'publicRoutes': '/fhir/R4', 'protectedRoutes': '/fhir/R4',
 'smartHealthLinkRouter': '/shl', 'mfaRouter': '/auth/mfa', 'bulkDataRouter': '/fhir/R4/bulkdata', 'jobRouter': '/fhir/R4/job', 'dicomRouter': '/dicomweb', 'fhircastSTU2Router': '/fhircast/STU2', 'fhircastSTU3Router': '/fhircast/STU3'}
RT = re.compile(r"\b(\w*(?:Router|Routes))\.(get|post|put|patch|delete|all)\(\s*(\[[^\]]*\]|'[^']*'|\"[^\"]*\"|`[^`]*`)", re.S)
OP = re.compile(r"router\.add\(\s*'(GET|POST|PUT|PATCH|DELETE)'\s*,\s*'([^']*)'\s*,\s*(\w+)")

def routes_in(path):
    txt = open(path, errors='ignore').read(); out = []
    for m in RT.finditer(txt):
        rv, meth, p = m.groups()
        if rv not in MOUNT: continue
        win = txt[m.end(): m.end() + 350].split(');')[0].split('async (')[0]
        paths = re.findall(r"'([^']*)'", p) if p.startswith('[') else [p.strip('\'"`')]
        for pp in paths:
            if '%24' in pp: continue
            out.append((meth.upper(), MOUNT[rv] + (pp if pp != '/' else '/'), 'Bearer' if ('authenticateRequest' in win or rv == 'protectedRoutes') else 'không guard ở route (xem middleware cha)'))
    return out

def gen_server():
    g, files = graph()
    inc = collections.defaultdict(collections.Counter)
    for s, c in g.items():
        for d, n in c.items(): inc[d][s] += n
    os.makedirs(f'{OUT}/modules/server', exist_ok=True)
    routes_by_mod = collections.defaultdict(list)
    for m, fl in files.items():
        for f in fl:
            for r in routes_in(f'{SRV}/{f}'): routes_by_mod[m].append(r + (f,))
    # operations trong fhir/routes.ts
    for meth, p, h in OP.findall(open(f'{SRV}/fhir/routes.ts').read()):
        routes_by_mod['fhir/operations'].append((meth, '/fhir/R4' + p, 'Bearer', h))
    summary = []
    for m, (desc, cluster) in SM.items():
        fl = sorted(files.get(m, []))
        out = ', '.join(f'`{k}` ({v})' for k, v in g[m].most_common()) or '—'
        inn = ', '.join(f'`{k}` ({v})' for k, v in inc[m].most_common()) or '—'
        rt = routes_by_mod.get(m, [])
        rtxt = '\n'.join(f'| {a} | `{b}` | {c} |' for a, b, c, *_ in sorted(set(r[:3] for r in rt), key=lambda x: (x[1], x[0]))) if rt else ''
        rsec = ('## Route\n\n| Method | Path | Xác thực |\n|---|---|---|\n' + rtxt + '\n\n') if rt else ''
        note = {'lõi': 'Thuộc cụm lõi: phải chuyển cùng `fhir`, `migrations`, `workers`, `config`, `storage`, `cloud` vì phụ thuộc vòng.',
                'danh tính': 'Cụm danh tính (`auth`, `oauth`, `admin`, `scim`): phụ thuộc `fhir` nặng. Giai đoạn 1 chuyển cùng server; giai đoạn 2 có thể thay bằng service Go identity.',
                'phụ': 'Cụm nhỏ, phụ thuộc một chiều vào `fhir` và root. Ứng viên tách sớm ở giai đoạn 2.'}[cluster]
        slug = m.replace('/', '-').strip('()')
        body = f"""# server/{m}

> {desc}

| | |
|---|---|
| Đường dẫn hiện tại | `medplum/packages/server/src/{m if m != '(root)' else ''}` |
| Đường dẫn đích | `apps/medplum-server/src/{m if m != '(root)' else ''}` |
| Số file nguồn (không tính test) | {len(fl)} |
| Cụm | {cluster} |

## Phụ thuộc (số import)

- Gọi tới: {out}
- Được gọi bởi: {inn}

{rsec}## Ghi chú tách

{note}

## File chính

{chr(10).join('- `' + f + '`' for f in fl[:25])}{chr(10) + '- … và ' + str(len(fl)-25) + ' file nữa' if len(fl) > 25 else ''}
"""
        open(f'{OUT}/modules/server/{slug}.md', 'w').write(body)
        summary.append((m, slug, cluster, len(fl), len(g[m]), len(inc[m])))
    return summary, routes_by_mod

# ---------------------------------------------------------------- API contract
AUTHFILE = {'/auth/method':'method','/auth/newuser':'newuser','/auth/newproject':'newproject','/auth/newpatient':'newpatient',
 '/auth/login':'login','/auth/profile':'profile','/auth/scope':'scope','/auth/changepassword':'changepassword',
 '/auth/resetpassword':'resetpassword','/auth/setpassword':'setpassword','/auth/verifyemail':'verifyemail',
 '/auth/google':'google','/auth/exchange':'exchange','/auth/revoke':'revoke','/auth/preauthorize':'preauthorize'}

def body_fields(f):
    p = f'{SRV}/auth/{f}.ts'
    if not os.path.exists(p): return []
    t = open(p).read()
    res = []
    for m in re.finditer(r"body\('(\w+)'\)([^,\]]*)", t):
        res.append((m.group(1), 'tùy chọn' if 'optional' in m.group(2) else 'bắt buộc'))
    seen = {}; [seen.setdefault(k, v) for k, v in res]
    return list(seen.items())

def gen_api(routes_by_mod):
    d = f'{OUT}/api-contract'; os.makedirs(d, exist_ok=True)
    def table(rows):
        rows = sorted(set(rows), key=lambda r: (r[1], r[0]))
        return '| Method | Path | Xác thực |\n|---|---|---|\n' + '\n'.join(f'| {a} | `{b}` | {c} |' for a, b, c in rows) + '\n'
    sel = lambda ms: [r[:3] for m in ms for r in routes_by_mod.get(m, [])]
    # FHIR REST chuẩn (fhir-router)
    rest = """# FHIR REST (R4)

Gốc: `/fhir/R4/` (cũng truy cập được qua `/api/projects/:projectId/fhir/R4/` và `/projects/:projectId/fhir/R4/`).
Nguồn: `packages/fhir-router/src/fhirrouter.ts`. Mọi route dưới đây cần `Authorization: Bearer <access_token>` (trừ `metadata` và `.well-known`).

| Method | Path | Interaction FHIR |
|---|---|---|
| GET | `/metadata` | capabilities (công khai) |
| GET | `/$versions` | versions (công khai) |
| GET | `/.well-known/smart-configuration` | SMART (công khai) |
| GET | `/` | search-system |
| POST | `/` | batch / transaction (body: Bundle) |
| GET | `/:resourceType` | search-type |
| POST | `/:resourceType/_search` | search-type (form-encoded) |
| POST | `/:resourceType` | create |
| GET | `/:resourceType/:id` | read |
| GET | `/:resourceType/:id/_history` | history-instance |
| GET | `/:resourceType/:id/_history/:vid` | vread |
| PUT | `/:resourceType/:id` | update |
| PUT | `/:resourceType?search` | conditional update |
| PATCH | `/:resourceType/:id` | patch (JSON Patch) |
| PATCH | `/:resourceType?search` | conditional patch |
| DELETE | `/:resourceType/:id` | delete |
| DELETE | `/:resourceType?search` | conditional delete |
| POST | `/$graphql` | GraphQL |
| GET | `/:resourceType/$csv` | xuất CSV |
| POST | `/$ai` | gọi AI |
| POST | `/Agent/$push`, `/Agent/:id/$push` | đẩy tin cho Agent |
| GET/POST | `/Bot/$execute`, `/Bot/:id/$execute`, `/Bot/:id/$execute/*` | chạy bot (cho phép thêm path phía sau) |
| GET | `/bulkdata/export/:id` | trạng thái và kết quả bulk export |
| GET | `/job/:id/status` | trạng thái AsyncJob |

## Quy ước

- Content-Type: `application/fhir+json` (hoặc `application/json`); `text/x-hl7-ft` cho HL7 v2 trên `$execute`.
- Lỗi trả `OperationOutcome` với HTTP 400, 401, 403, 404, 409, 410, 412, 422, 429, 500.
- Rate limit: mọi response có header `RateLimit: "requests";r=<còn lại>;t=<giây tới reset>`. Khi vượt hạn mức trả `429` với `OperationOutcome` (diagnostics chứa JSON hạn mức, kèm thời gian reset). Giới hạn theo nhóm (login, MFA, FHIR) và theo project.
- Phân trang search: `_count`, `_offset`, `link[rel=next]`; sắp xếp `_sort`; `_total`. Các tham số này theo chuẩn FHIR; chi tiết hỗ trợ xem `GET /fhir/R4/metadata`.
- Header chuẩn FHIR (`ETag`, `Location`, `Prefer`, `If-Match`, `If-None-Exist`) được hỗ trợ ở mức router; xác minh từng header trong `fhir-router` trước khi dựa vào.
"""
    open(f'{d}/fhir-rest.md', 'w').write(rest)
    ops = sorted(set(r[:3] for r in routes_by_mod['fhir/operations']), key=lambda r: (r[1], r[0]))
    open(f'{d}/fhir-operations.md', 'w').write(f"""# FHIR Operations (`$...`)

Sinh tự động từ `packages/server/src/fhir/routes.ts` ({len(ops)} route). Mỗi operation có `OperationDefinition` mô tả tham số; input nhận `Parameters` hoặc JSON phẳng.

{table(ops)}
Nhóm theo mục đích: terminology (`ValueSet/$expand`, `CodeSystem/$lookup|$validate-code|$subsumes|$import`, `ConceptMap/$translate|$import`), lâm sàng (`Patient/$everything|$summary|$match|$ccda-export`, `$extract`, `$evaluate-measure`, `PlanDefinition/$apply`), lịch hẹn (`Appointment/$find|$book|$hold|$cancel|$confirm|$reschedule`), dữ liệu lớn (`$export`, `Group/$export`, `Patient/$export`), vận hành project (`Project/$init|$clone|$archive`, `$restore`), agent (`Agent/$status|$stats|$upgrade|$fetch-logs|$reload-config|$bulk-status|$push`), bot (`Bot/$init|$deploy|$execute`), hạ tầng DB (`$db-*`, `$explain`).
""")
    # Auth
    a = [r[:3] for r in routes_by_mod.get('auth', [])]
    s = f"# Auth API\n\nGốc: `/auth/`. Nguồn: `server/src/auth/routes.ts`.\n\n{table(a)}\n## Body theo endpoint (trích từ validator)\n\nBảng chỉ liệt kê trường có khai báo validator; handler có thể đọc thêm. Ví dụ `/auth/login` còn nhận `clientId`, `scope`, `nonce`, `codeChallenge`, `codeChallengeMethod`, `launch`, `resourceType`, `remember` (xem `auth/login.ts`); các route có `validateRecaptcha` (`newuser`, `resetpassword`) còn đọc `recaptchaSiteKey` và token reCAPTCHA từ body (xem `validateRecaptcha` trong `auth/utils.ts`).\n\n"
    for ep, f in AUTHFILE.items():
        fields = body_fields(f)
        if fields:
            s += f"### POST `{ep}`\n\n| Trường | |\n|---|---|\n" + '\n'.join(f'| `{k}` | {v} |' for k, v in fields) + '\n\n'
    s += """## Luồng phản hồi đăng nhập

Nguồn: `sendLoginResult` trong `server/src/auth/utils.ts`. `/auth/login` (và các bước sau) trả một trong các dạng:

| Tình huống | Phản hồi |
|---|---|
| Project yêu cầu MFA nhưng user chưa đăng ký | `{ login, mfaEnrollRequired: true, enrollUri, enrollQrCode, allowedMfaMethods }` |
| User đã đăng ký MFA, chưa xác minh | `{ login, mfaRequired: true, mfaMethods, email }` (gọi `/auth/mfa/...` để xác minh) |
| Đang tạo project mới (`projectId = new`) | `{ login }` rồi gọi `/auth/newproject` |
| Chỉ có một profile | `{ login, code }` (kèm cookie đăng nhập với client OAuth) |
| Nhiều profile | `{ login, memberships: [{ id, project, profile, identifier }] }` rồi gọi `/auth/profile` |

Sau đó đổi `code` lấy token ở `POST /oauth2/token` (`grant_type=authorization_code`, kèm `code_verifier` nếu đã gửi `codeChallenge`).
"""
    open(f'{d}/auth.md', 'w').write(s)
    # OAuth
    o = [r[:3] for r in routes_by_mod.get('oauth', [])] + [r[:3] for r in routes_by_mod.get('(root)', []) if r[1].startswith('/.well-known')]
    open(f'{d}/oauth.md', 'w').write(f"""# OAuth2 / OpenID Connect

Gốc: `/oauth2/`. Nguồn: `server/src/oauth/routes.ts`, `wellknown.ts`.

{table(o)}
## `POST /oauth2/token`

Content-Type `application/x-www-form-urlencoded` (hoặc JSON). Trường `grant_type` bắt buộc, hỗ trợ:

| grant_type | Dùng cho | Tham số chính |
|---|---|---|
| `authorization_code` | ứng dụng người dùng (PKCE) | `code`, `redirect_uri`, `code_verifier`, `client_id` |
| `refresh_token` | gia hạn | `refresh_token`, `client_id` |
| `client_credentials` | máy với máy | `client_id`, `client_secret` (xem `oauth/token.ts` cho các cách xác thực client khác) |
| `urn:ietf:params:oauth:grant-type:token-exchange` | đổi token ngoài lấy token Medplum | `subject_token`, `subject_token_type` |
| `pre-authorized_code` | cấp quyền trước | `pre-authorized_code` |

Phản hồi: `{{ access_token, refresh_token, id_token, token_type: "Bearer", expires_in, scope, project, profile }}`.
Lỗi theo RFC 6749: `{{ error, error_description }}`.
""")
    # Admin
    ad = sel(['admin'])
    open(f'{d}/admin.md', 'w').write(f"""# Admin API

Gốc: `/admin/`. Nguồn: `server/src/admin/`. Project admin cần quyền admin của project; super admin cần cờ `superAdmin`.

{table(ad)}
Nhóm: mời user (`invite`), tạo `bot` và `client`, quản lý `members`, `settings`, `secrets`, `sites`; super admin: `reindex`, `rebuild-index`, `purge`, `vacuum`, `migrate`, `setpassword`, `tablesettings`, `reloadcron`, `valuesets`, `structuredefinitions`, `searchparameters`.
""")
    # Khác
    ot = [r[:3] for m in ('cds','email','keyvalue','webhook','mcp','scim','fhircast','dicom','fhir','(root)') for r in routes_by_mod.get(m, []) if not r[1].startswith(('/fhir/R4/$','/fhir/R4/:')) and not r[1].startswith('/auth')]
    ot = [r for r in ot if not r[1].startswith(('/oauth2','/admin'))]
    open(f'{d}/others.md', 'w').write(f"""# API khác

CDS Hooks, email, key-value, webhook, MCP, SCIM, FHIRcast, well-known. Nguồn: các `routes.ts` trong từng module.

{table(ot)}
WebSocket (`server/src/ws/routes.ts`): handler chọn theo phân đoạn thứ hai của URL, gồm `/ws/subscriptions-r4`, `/ws/agent`, `/ws/fhircast`, `/ws/ai-realtime`, `/ws/echo` (tiền tố `/api` và `/projects/:projectId` được chuẩn hóa).

DICOMweb (`server/src/dicom`): gốc `/dicomweb/` (cũng qua `/api/dicomweb/` và `/projects/:projectId/dicomweb/`).

| Method | Path | Trạng thái |
|---|---|---|
| POST | `/dicomweb/studies`, `/dicomweb/studies/:studyUid` | STOW-RS lưu instance |
| GET | `/dicomweb/studies` | QIDO-RS tìm study |
| GET | `/dicomweb/studies/:studyUid/series` | QIDO-RS tìm series |
| GET | `/dicomweb/studies/:studyUid/series/:seriesUid/metadata` | WADO-RS metadata series |
| GET | các route còn lại (`rendered`, `instances`, ...) | chưa cài (stub `notImplemented`) |
""")
    open(f'{d}/README.md', 'w').write("""# API Contract

Hợp đồng API của `medplum-server`. Các bảng route được **sinh tự động từ mã nguồn** (`docs/_generator/gen_docs.py`, chạy lại được khi còn thư mục `medplum/`); không sửa tay.

Bản đầy đủ, luôn khớp code, do chính server phát ra lúc chạy:

- `GET /.well-known/api-catalog`: danh mục API.
- OpenAPI: sinh từ `server/src/openapi.ts` và `OperationDefinition`.
- `GET /fhir/R4/metadata`: CapabilityStatement.

| Tài liệu | Phạm vi |
|---|---|
| [fhir-rest.md](fhir-rest.md) | CRUD, search, history, batch, GraphQL |
| [fhir-operations.md](fhir-operations.md) | 113 operation `$...` |
| [auth.md](auth.md) | Đăng ký, đăng nhập, MFA, mật khẩu |
| [oauth.md](oauth.md) | OAuth2/OIDC, token, well-known |
| [admin.md](admin.md) | Quản trị project và super admin |
| [others.md](others.md) | CDS, email, keyvalue, webhook, MCP, SCIM, FHIRcast, WebSocket, DICOM |

## Quy ước chung

- Gốc có thể kèm tiền tố `/api/` hoặc `/projects/:projectId/` (cùng một router).
- Xác thực: `Authorization: Bearer <access_token>` (OAuth2). Tài khoản máy dùng `client_credentials`.
- Lỗi FHIR: `OperationOutcome`. Lỗi OAuth: `{error, error_description}`. Lỗi auth/admin: `OperationOutcome`.
- Giới hạn: rate limit theo IP và theo project (`429`), kích thước JSON theo `maxJsonSize`.
""")

def gen_index(prows, srows):
    L = ['# Mục lục module medplum\n', 'Sinh tự động. Mỗi trang gồm vai trò, đường dẫn hiện tại và đích, phụ thuộc, thứ tự di chuyển.\n',
         '## Package (`packages/`)\n', '| Package | Loại | Đích | File | Phụ thuộc runtime |', '|---|---|---|---|---|']
    for d, name, kind, dest, n, rt in prows:
        L.append(f"| [`{name}`](packages/{d}.md) | {kind} | `{dest}` | {n} | {', '.join(rt) or '—'} |")
    L += ['', '## Module trong `server/src`\n', '| Module | Cụm | File | Gọi tới | Được gọi bởi |', '|---|---|---|---|---|']
    for m, slug, cl, n, o, i in srows:
        L.append(f"| [`{m}`](server/{slug}.md) | {cl} | {n} | {o} module | {i} module |")
    open(f'{OUT}/modules/README.md', 'w').write('\n'.join(L) + '\n')

if __name__ == '__main__':
    prows = gen_packages()
    srows, routes = gen_server()
    gen_api(routes)
    gen_index(prows, srows)
    print(len(prows), 'packages', len(srows), 'server modules', sum(len(v) for v in routes.values()), 'routes')
