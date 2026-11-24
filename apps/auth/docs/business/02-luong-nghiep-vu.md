# 2. Luồng nghiệp vụ đầu-cuối

Mỗi luồng nêu **mục tiêu nghiệp vụ**, **các bên**, **sơ đồ**, **kết quả** và **chức năng tham gia** (mã F-xx ở [04-chuc-nang.md](04-chuc-nang.md)).

## Tổng quan vòng đời

```mermaid
flowchart LR
    A["BF-1<br/>Mở tổ chức"] --> B["BF-2<br/>Mời / đăng ký thành viên"]
    B --> C["BF-3<br/>Đăng nhập và nhận token"]
    C --> D["BF-4<br/>Truy cập dữ liệu FHIR"]
    D --> E["BF-5<br/>Duy trì phiên (refresh)"]
    E --> D
    C --> F["BF-6<br/>Bảo mật tài khoản<br/>(MFA, mật khẩu)"]
    D --> G["BF-7<br/>Thu hồi quyền"]
    G --> H["BF-8<br/>Nghỉ việc / xóa thành viên"]
    I["BF-9<br/>Tích hợp doanh nghiệp<br/>(SCIM, IdP, client máy)"] -.-> B
    I -.-> C
```

---

## BF-1. Mở tổ chức (onboarding project)

**Mục tiêu:** một bệnh viện/phòng khám có không gian dữ liệu riêng và có admin đầu tiên.
**Hai đường:** (a) **vận hành** seed bằng CLI (khách hàng B2B do Trivita triển khai); (b) **tự phục vụ** `newuser` → `newproject` (nếu server bật đăng ký).

```mermaid
flowchart TD
    S([Bắt đầu]) --> Q{Ai khởi tạo?}
    Q -->|"Vận hành"| CLI["auth seed project<br/>(1 transaction)"]
    CLI --> R1["Tạo: admin user, 6 AccessPolicy mặc định,<br/>Project strictMode, client mặc định,<br/>Practitioner và membership admin"]
    Q -->|"Khách tự đăng ký"| NU["POST /auth/newuser<br/>(reCAPTCHA, HIBP, trùng email)"]
    NU --> V{"Server bật<br/>đăng ký?"}
    V -->|Không| E1["400 Registration is disabled"]
    V -->|Có| L["Tạo User + Login chưa có membership<br/>Gửi thư xác minh email"]
    L --> NP["POST /auth/newproject<br/>(tên 4-72 ký tự)"]
    NP --> C1{"Login hợp lệ?<br/>không thu hồi, chưa cấp code,<br/>chưa có membership"}
    C1 -->|Không| E2["400 theo từng lý do<br/>(chưa tạo gì)"]
    C1 -->|Có| C2{"Cần MFA<br/>mà chưa qua?"}
    C2 -->|Có| M["Trả mfaRequired / mfaEnrollRequired<br/>(chưa tạo gì)"]
    C2 -->|Không| C3{"Bắt buộc email<br/>đã xác minh?"}
    C3 -->|"Có, chưa xác minh"| E3["400 + gửi lại thư xác minh"]
    C3 -->|Ổn| R2["Tạo trong 1 transaction:<br/>Project, 6 AccessPolicy, client mặc định,<br/>Practitioner, membership admin"]
    R2 --> W["Gửi thư chào chủ project lần đầu"]
    R1 --> OK
    W --> OK(["Có tổ chức + admin<br/>Trả code để vào hệ thống"])
```

**Kết quả:** Project `active`, admin đăng nhập được. **Tính nguyên tử:** lỗi giữa chừng hoàn tác hết, không có project dở dang. **Chức năng:** F-01, F-02, F-04, F-28.

---

## BF-2. Đưa thành viên vào tổ chức

**Mục tiêu:** đúng người có đúng hồ sơ và đúng quyền.

```mermaid
flowchart TD
    subgraph Duong1["Đường 1: Admin mời"]
        I1["Admin gọi POST invite<br/>(Practitioner / Patient / RelatedPerson)"]
        I2{"Kiểm tra"}
        I3["Ghi User + hồ sơ + membership<br/>trong 1 transaction"]
        I4["Gửi thư đặt mật khẩu<br/>(nếu sendEmail)"]
    end
    subgraph Duong2["Đường 2: Bệnh nhân tự đăng ký"]
        P1["newuser với projectId"]
        P2{"Project mở đăng ký bệnh nhân<br/>và có chính sách Patient?"}
        P3["POST newpatient<br/>tạo Patient + membership"]
    end
    subgraph Duong3["Đường 3: IdP đồng bộ"]
        S1["SCIM POST /Users"]
        S2["Đi cùng đường invite<br/>(không gửi thư)"]
    end
    I1 --> I2
    I2 -->|"Email miền bị chặn,<br/>trùng externalId, đã là member"| X["400 / 409<br/>(chưa ghi gì)"]
    I2 -->|Hợp lệ| I3 --> I4 --> DONE
    P1 --> P2
    P2 -->|Không| X2["400 Project does not allow open registration"]
    P2 -->|Có| P3 --> DONE
    S1 --> S2 --> DONE
    DONE(["Thành viên có membership active<br/>+ AccessPolicy mặc định theo vai"])
```

**Luật then chốt:** Practitioner có email bị chặn theo `AUTH_BLOCKED_EMAIL_DOMAINS` hoặc setting `allowedPractitionerEmailDomain`; AccessPolicy mặc định lấy từ `defaultPatientAccessPolicy`/`defaultAccessPolicies` (Admin cho Practitioner có `admin`); user phạm vi project phải thuộc đúng project. **Chức năng:** F-03, F-24, F-25, F-26.

> **Điểm còn hở (đã ghi nhận):** mời bệnh nhân tạo `Patient` trong `identity`; muốn `patient/` compartment hoạt động phải tạo `Patient` cùng id ở `fhir-server` (thủ công, xem runbook §12).

---

## BF-3. Đăng nhập và nhận token (luồng cốt lõi)

**Mục tiêu:** người dùng chứng minh danh tính, chọn hồ sơ, ứng dụng nhận token đúng quyền.
**Bốn cách chứng minh:** mật khẩu, Google, IdP doanh nghiệp (OIDC), hoặc mã cấp sẵn (không người). Dưới đây là đường mật khẩu + SPA.

```mermaid
sequenceDiagram
    autonumber
    actor U as Người dùng
    participant APP as Ứng dụng (SPA)
    participant A as auth
    participant M as Kho trạng thái MFA (Redis)
    participant F as fhir-server

    U->>APP: Mở ứng dụng
    APP->>APP: Sinh code_verifier, code_challenge (S256)
    APP->>A: POST /auth/login (email, mật khẩu, clientId, codeChallenge, redirectUri)
    A->>A: Kiểm hạn mức IP, mật khẩu (bcrypt), client, project
    alt Sai thông tin
        A-->>APP: 400 (thông điệp chung, không lộ tài khoản có tồn tại)
    else Cần MFA, chưa đăng ký
        A-->>APP: mfaEnrollRequired (enrollUri, QR)
        U->>APP: Quét QR, nhập mã
        APP->>A: POST /auth/mfa/login-enroll
    else Đã đăng ký MFA
        A-->>APP: mfaRequired (mfaMethods)
        U->>APP: Nhập mã TOTP hoặc mã email
        APP->>A: POST /auth/mfa/verify
        A->>M: Đặt chỗ lượt thử (5/login, 20/user)
    end
    alt Nhiều hồ sơ
        A-->>APP: memberships[]
        U->>APP: Chọn hồ sơ
        APP->>A: POST /auth/profile
    end
    A-->>APP: code (một lần, 10 phút) + cookie đăng nhập
    APP->>A: POST /oauth2/token (code, code_verifier, redirect_uri)
    A->>A: Kiểm PKCE, MFA đã xong, IP rule, dịch AccessPolicy
    A-->>APP: access_token (15 phút), refresh_token, id_token
    APP->>F: Gọi API FHIR, Authorization Bearer
    F->>A: GET /.well-known/jwks.json (cache 10 phút)
    F-->>APP: Dữ liệu của đúng project, đúng quyền
```

**Kết quả:** token mang `fhir_project`, `scope`, `fhir_access_policy`. **Nguyên tắc:** MFA chưa xong thì **không bao giờ** cấp code (fail đóng). **Chức năng:** F-05..F-10, F-15, F-16, F-22.

### Biến thể: ứng dụng chuyển hướng qua `/oauth2/authorize`

```mermaid
flowchart TD
    A["Ứng dụng chuyển trình duyệt tới<br/>/oauth2/authorize"] --> B{"client_id và<br/>redirect_uri hợp lệ?"}
    B -->|Không| E1["400 văn bản<br/>(không chuyển hướng, tránh open redirect)"]
    B -->|Có| C{"Tham số khác hợp lệ?<br/>response_type, PKCE, prompt..."}
    C -->|Không| E2["302 về redirect_uri<br/>kèm error, state, iss"]
    C -->|Có| D{"Đã có phiên?<br/>cookie hoặc id_token_hint"}
    D -->|"Không, hoặc prompt=login"| G["302 sang AUTH_APP_BASE_URL/oauth<br/>(ứng dụng đăng nhập)"]
    D -->|Có| H{"prompt=none?"}
    H -->|Có| I["Tạo Login mới (basedOn cũ)<br/>trả code ngay: SSO không hỏi lại"]
    H -->|Không| J["Sang trang ứng dụng kèm login=id"]
    G --> K["Đăng nhập như BF-3, rồi về redirect_uri kèm code"]
    J --> K
```

### Biến thể: đăng nhập IdP doanh nghiệp / Google

```mermaid
sequenceDiagram
    autonumber
    actor U as Nhân viên
    participant APP as Ứng dụng
    participant A as auth
    participant IDP as IdP (OIDC / Google)
    U->>APP: Nhập email
    APP->>A: POST /auth/method (email, clientId, PKCE...)
    A->>A: Tra DomainConfiguration theo miền email
    alt Miền dùng IdP
        A-->>APP: domain, authorizeUrl (đã kèm state ký HMAC) + cookie auth-external-state
        APP->>IDP: Chuyển hướng người dùng
        IDP-->>A: GET /auth/external?code&state
        A->>A: Kiểm state đúng trình duyệt, đổi code, xác minh id_token bằng JWKS
        A->>A: Email IdP phải đã xác minh, đúng miền, có membership
        A-->>APP: 302 về ứng dụng kèm code
    else Miền dùng mật khẩu
        A-->>APP: {} (tiếp tục BF-3 bằng mật khẩu)
    end
```

---

## BF-4. Truy cập dữ liệu FHIR bằng token

**Mục tiêu:** `fhir-server` thi hành quyền mà **không gọi lại auth** mỗi yêu cầu.

```mermaid
flowchart LR
    T["Access token JWT"] --> V["fhir-server<br/>xác minh chữ ký (JWKS),<br/>iss, aud, hạn"]
    V --> P["Đọc claim<br/>fhir_project, scope,<br/>fhir_access_policy"]
    P --> ISO["Cách ly theo<br/>fhir_project"]
    P --> AP["Lọc theo từng rule:<br/>loại resource, criteria,<br/>readonly, deny"]
    ISO & AP --> R["Chỉ trả resource được phép"]
```

**Hệ quả nghiệp vụ cần BA nắm:** thu hồi quyền chỉ có hiệu lực ở FHIR **sau tối đa 15 phút** (`AUTH_ACCESS_TTL`, quyết định D-13); các route của auth thì từ chối ngay. Đây là đánh đổi *hiệu năng đổi lấy độ trễ thu hồi*. **Chức năng:** F-16, F-22.

---

## BF-5. Duy trì phiên (refresh)

```mermaid
flowchart TD
    S["Access token sắp hết hạn"] --> R["POST /oauth2/token<br/>grant=refresh_token"]
    R --> C{"Refresh secret<br/>khớp bản hiện tại?"}
    C -->|Có| N["Xoay secret (ghi có điều kiện phiên bản)<br/>cấp access và refresh token mới"]
    C -->|"Bản đã xoay"| G{"Trong 30 giây<br/>và cùng IP?"}
    G -->|Có| N2["Chấp nhận (mạng chập chờn, gọi song song)"]
    G -->|Không| X["THU HỒI Login<br/>invalid_grant: nghi lộ token"]
    C -->|"Không khớp"| X2["invalid_grant"]
    N --> OK(["Phiên kéo dài, không nhập lại mật khẩu"])
    N2 --> OK
```

**Ý nghĩa:** phát hiện đánh cắp refresh token: kẻ cắp và người thật cùng dùng thì một bên dùng secret cũ, cả phiên bị thu hồi. **Chức năng:** F-17.

---

## BF-6. Bảo mật tài khoản

```mermaid
flowchart TD
    subgraph MK["Mật khẩu"]
        M1["Đang đăng nhập: changepassword<br/>(cần mật khẩu cũ)"]
        M2["Quên: resetpassword → thư → setpassword"]
    end
    subgraph MFA["MFA"]
        F1["Bật: GET status → enroll<br/>(TOTP hoặc email)"]
        F2["Tắt: disable (cần mã)<br/>không tắt được yếu tố cuối nếu bắt buộc"]
        F3["Mất thiết bị: admin mfa/reset"]
    end
    subgraph PH["Phiên"]
        S1["Xem phiên: GET /auth/me<br/>(trình duyệt, hệ điều hành, IP)"]
        S2["Đăng xuất một phiên: revoke"]
    end
    M2 --> SP["setpassword ⇒ xác minh email<br/>VÀ thu hồi mọi Login của user"]
    M1 --> OK1(["Mật khẩu mới<br/>(kiểm HIBP)"])
    SP --> OK1
```

**Chức năng:** F-11, F-12, F-13, F-14, F-25.

---

## BF-7. Thu hồi quyền

Các cách chấm dứt quyền và **độ trễ hiệu lực** (đây là bảng BA/QA/bảo mật cần thống nhất):

| Sự kiện kích hoạt | Tác động lên auth | Tác động lên fhir-server |
|---|---|---|
| Người dùng `logout` / `revoke` | Login bị thu hồi **ngay**; refresh, userinfo, introspect, `/auth/me` 401 | Access token còn sống đến hết ≤ 15 phút |
| Đổi/đặt lại mật khẩu (`setpassword`) | **Mọi** Login của user bị thu hồi | như trên |
| Dùng lại code | Login bị thu hồi | như trên |
| Dùng lại refresh secret cũ (ngoài ân hạn hoặc khác IP) | Login bị thu hồi, cảnh báo log | như trên |
| Sai MFA lần thứ 5 | Login bị thu hồi | — |
| Admin tắt `active` hoặc xóa membership | Token của member bị từ chối ngay ở route auth | ≤ 15 phút |
| Admin gỡ liên kết bệnh nhân (compartment) | — | Token cũ đọc được đến hết hạn |
| Xoay khóa ký, xóa khóa cũ | Token ký khóa cũ không xác minh được sau khi xóa | Từ chối sau khi cache JWKS hết (≤ 10 phút) |

**Chức năng:** F-10, F-13, F-14, F-17, F-21, F-25, F-28.

---

## BF-8. Nghỉ việc / xóa thành viên

```mermaid
flowchart TD
    T{"Kích hoạt"} -->|"Admin: DELETE members/{id}"| C
    T -->|"IdP: SCIM active=false / DELETE"| C
    C{"Admin API: chủ project (khi xóa)<br/>hay admin cuối cùng?<br/>SCIM: thêm chính người gọi, chủ (khi tắt)"}
    C -->|Có| E["400 chặn: không thể mất quyền quản trị"]
    C -->|Không| D["Tắt hoặc xóa membership"]
    D --> U{"User thuộc project<br/>và hết membership?"}
    U -->|Có| DU["Xóa luôn User"]
    U -->|"Không (user server-scope)"| K["Giữ User"]
    D --> L["Token của member bị từ chối ngay ở route auth"]
    L --> Z(["Quyền FHIR hết sau ≤ 15 phút"])
```

SCIM `active=true` cho membership đã tắt được chấp nhận (IdP là nguồn sự thật) và ghi log kiểm toán. **Chức năng:** F-25, F-26.

---

## BF-9. Tích hợp doanh nghiệp

| Nhu cầu khách hàng | Giải pháp | Luồng |
|---|---|---|
| Nhân viên đăng nhập bằng tài khoản công ty | `auth domain add` + IdP OIDC | BF-3 biến thể IdP |
| IdP tự tạo/khóa tài khoản | SCIM 2.0 `/scim/v2/Users` | Okta/Azure gọi SCIM; membership đồng bộ |
| Hệ thống khác gọi API không có người | Client `client_credentials` + `private_key_jwt` | `POST /oauth2/token` |
| Backend của đối tác thay mặt người dùng | `preauthorize` → `pre-authorized_code` | Admin cấp mã, đối tác đổi token |
| Ứng dụng đã có token IdP, muốn token Trivita | `token-exchange` | `POST /oauth2/token` grant token-exchange |
| Ứng dụng y tế SMART/FHIRcast | scope `launch`, `patient/`, `fhircast/` | Token kèm `hub.url`, `hub.topic` |

```mermaid
sequenceDiagram
    autonumber
    participant ADM as Admin project
    participant A as auth
    participant PART as Backend đối tác
    ADM->>A: POST /auth/preauthorize (Bearer, header X-Medplum-On-Behalf-Of, clientId, scope ⊆ scope admin)
    A-->>ADM: preAuthorizedCode (lưu băm, một lần), expiresAt
    ADM->>PART: Chuyển mã qua kênh an toàn
    PART->>A: POST /oauth2/token grant=pre-authorized_code
    A-->>PART: Token thay mặt membership đó
    Note over A: Dùng lại mã ⇒ thu hồi cả token đã cấp
```

**Chức năng:** F-09, F-16, F-18, F-19, F-20, F-26.
