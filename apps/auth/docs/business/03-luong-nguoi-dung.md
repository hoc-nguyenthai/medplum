# 3. Luồng người dùng (user flow)

Góc nhìn của **từng nhân vật** đi qua các bước trên giao diện của *ứng dụng đăng nhập* (auth chỉ cung cấp API; màn hình do ứng dụng ở `AUTH_APP_BASE_URL` dựng). Mỗi luồng ghi rõ **bước màn hình → API gọi → thông điệp cho người dùng**, để UX/FE/QA cùng một bản đồ. Mã F-xx tham chiếu [04-chuc-nang.md](04-chuc-nang.md).

## 3.1 Bản đồ màn hình

```mermaid
flowchart LR
    S1["Màn hình<br/>Nhập email"] --> S2["Nhập mật khẩu"]
    S1 --> S2b["Chuyển sang IdP<br/>(đăng nhập công ty)"]
    S2 --> S3["Nhập mã MFA"]
    S2 --> S3b["Đăng ký MFA<br/>(quét QR)"]
    S3 --> S4["Chọn hồ sơ"]
    S3b --> S4
    S2 --> S4
    S4 --> S5["Chọn quyền (scope)<br/>nếu ứng dụng yêu cầu"]
    S4 --> OUT(["Về ứng dụng<br/>kèm code"])
    S5 --> OUT
    S1 --> R1["Đăng ký"]
    R1 --> R2["Báo kiểm tra email"]
    R2 --> R3["Tạo tổ chức"]
    S2 --> P1["Quên mật khẩu"]
    P1 --> P2["Báo kiểm tra email"]
    P2 --> P3["Đặt mật khẩu mới"]
```

## 3.2 Đăng nhập (mọi người dùng) — F-05, F-06, F-07, F-10

```mermaid
flowchart TD
    Start([Mở ứng dụng]) --> E["Màn: nhập email"]
    E -->|"POST /auth/method"| M{"Miền email<br/>có IdP riêng?"}
    M -->|Có| IDP["Chuyển sang trang đăng nhập công ty<br/>(xem 3.7)"]
    M -->|Không| PW["Màn: nhập mật khẩu"]
    PW -->|"POST /auth/login"| R{"Kết quả"}
    R -->|"400 sai thông tin"| PWE["Báo lỗi chung<br/>'Email or password is invalid'<br/>(không nói email có tồn tại)"]
    PWE --> PW
    R -->|"429 quá nhiều lần"| TH["Báo chờ Retry-After giây"]
    R -->|mfaEnrollRequired| EN["Màn: đăng ký MFA (3.5)"]
    R -->|mfaRequired| MF["Màn: nhập mã MFA (3.5)"]
    R -->|memberships| CH["Màn: chọn hồ sơ"]
    R -->|code| DONE
    CH -->|"POST /auth/profile"| R2{"Còn cần scope?"}
    R2 -->|Có| SC["Màn: đồng ý quyền<br/>POST /auth/scope"] --> DONE
    R2 -->|Không| DONE
    MF --> CH
    EN --> CH
    DONE(["Chuyển về redirect_uri kèm code<br/>ứng dụng đổi token"])
```

**Thông điệp và hành vi UX cần thống nhất**

| Tình huống | Người dùng thấy | Lý do thiết kế |
|---|---|---|
| Sai mật khẩu hoặc email không tồn tại | Cùng một thông điệp | Không lộ tài khoản có tồn tại (chống dò) |
| Bị giới hạn tốc độ | "Thử lại sau N giây" (`Retry-After`) | Chống đoán mật khẩu (khoảng 60 lần/phút/IP) |
| Login để quá lâu giữa các bước MFA | "Login expired", quay lại nhập mật khẩu | Login chỉ dùng xác minh MFA trong 10 phút |
| Chỉ có một hồ sơ | Bỏ qua màn chọn hồ sơ | Server trả `code` thẳng |
| Lỗi hạ tầng (Redis, HIBP...) | Lỗi chung 500 | Fail đóng: không bao giờ cho qua MFA/kiểm tra bảo mật khi lỗi |

## 3.3 Đăng ký người dùng mới và tổ chức mới — F-01, F-04, F-02

Nhân vật: **Khách (A1)**.

```mermaid
flowchart TD
    A([Khách chọn Đăng ký]) --> F["Màn: họ, tên, email, mật khẩu<br/>+ reCAPTCHA"]
    F -->|"POST /auth/newuser"| V{"Kiểm tra"}
    V -->|"Recaptcha lỗi / thiếu"| E1["Báo lỗi reCAPTCHA"]
    V -->|"Email đã đăng ký"| E2["Báo 'Email already registered' (trường email)"]
    V -->|"Mật khẩu có trong dữ liệu rò rỉ"| E3["Báo 'Password found in breach database'"]
    V -->|"Server tắt đăng ký"| E4["Báo 'Registration is disabled'"]
    V -->|OK| W["Màn: Kiểm tra email để xác minh<br/>(vẫn tiếp tục được)"]
    W --> T{"Muốn làm gì?"}
    T -->|"Tạo tổ chức"| NP["Màn: tên tổ chức (4-72 ký tự)<br/>POST /auth/newproject"]
    T -->|"Tham gia tổ chức có mở đăng ký"| NPA["POST /auth/newpatient"]
    NP --> G{"Tổ chức yêu cầu email đã xác minh?"}
    G -->|"Có, chưa xác minh"| E5["Báo 'Email verification is required...'<br/>+ gửi lại thư"]
    G -->|Ổn| MF{"Cần MFA?"}
    MF -->|Có| M["Đi luồng MFA (3.5)"] --> OK
    MF -->|Không| OK(["Vào hệ thống với quyền admin tổ chức vừa tạo"])
    NPA --> OK2(["Vào hệ thống với hồ sơ Patient"])
    X["Người dùng bấm link trong thư<br/>(7 ngày)"] -->|"POST /auth/verifyemail"| XV["Email đã xác minh"]
```

Lưu ý cho FE: client công khai phải gửi `codeChallenge` **ngay ở bước `newuser`** thì mới đổi được code sau `newproject`. Phản hồi `newuser` không có `code`, chỉ có `login` và `emailVerificationRequired`.

## 3.4 Bệnh nhân / người nhà — F-03, F-06, F-14

```mermaid
flowchart TD
    A([Bệnh nhân]) --> H{"Có tài khoản?"}
    H -->|"Được phòng khám mời"| M["Nhận thư → link đặt mật khẩu<br/>(setpassword)"]
    H -->|"Tự đăng ký<br/>(phòng khám mở đăng ký)"| R["Đăng ký 3.3 với projectId của phòng khám<br/>rồi newpatient"]
    M --> L["Đăng nhập 3.2"]
    R --> L
    L --> APP["Ứng dụng bệnh nhân<br/>xem dữ liệu CỦA MÌNH"]
    APP --> SEC["Tài khoản của tôi:<br/>GET /auth/me"]
    SEC --> S1["Xem các phiên đang đăng nhập"]
    SEC --> S2["Đăng xuất phiên lạ: POST /auth/revoke"]
    SEC --> S3["Đổi mật khẩu 3.6"]
    SEC --> S4["Bật MFA 3.5"]
```

Quyền của bệnh nhân bị giới hạn trong *compartment* của chính họ (token chỉ có tiêu chí `_compartment=Patient/<id>`); không đọc được bệnh nhân khác kể cả qua `_include`, `$everything`. Mặc định chính sách Patient **cho phép ghi** trong compartment của mình; muốn chỉ đọc đặt `readonly: true` (runbook §12).

## 3.5 MFA — F-10, F-11

### a) Đăng ký MFA khi đăng nhập (bị bắt buộc)

```mermaid
flowchart TD
    A["login trả mfaEnrollRequired<br/>(enrollUri, QR, allowedMfaMethods)"] --> B{"Chọn phương thức"}
    B -->|"Ứng dụng xác thực (TOTP)"| T["Màn: quét QR, nhập mã 6 số"]
    B -->|"Email"| E["Màn: gửi mã tới email, nhập mã"]
    T -->|"POST /auth/mfa/login-enroll"| R{"Mã đúng?"}
    E -->|"POST /auth/mfa/login-enroll"| R
    R -->|Đúng| OK["Đăng ký xong, tiếp tục chọn hồ sơ"]
    R -->|"Sai"| W["Báo 'Invalid MFA token'<br/>đếm lượt thử"]
    W --> L{"Lần thứ 5?"}
    L -->|Có| REV["Login bị thu hồi: đăng nhập lại từ đầu"]
    L -->|Chưa| R
```

### b) Xác minh MFA mỗi lần đăng nhập

```mermaid
flowchart TD
    A["login trả mfaRequired + mfaMethods"] --> B{"Có TOTP?"}
    B -->|Có| T["Nhập mã từ ứng dụng"]
    B -->|"Chỉ có email"| E["Server tự gửi mã (một lần)"]
    E --> I["Nhập mã email"]
    T & I -->|"POST /auth/mfa/verify"| R{"Kết quả"}
    R -->|Đúng| OK["Tiếp tục chọn hồ sơ / nhận code"]
    R -->|"Sai"| W["'Invalid MFA token' (cùng thông điệp cho cả hai loại)"]
    R -->|"Mã email hết hạn (20 phút)"| X["'MFA code expired' → gửi lại: POST /auth/mfa/send-email"]
    R -->|"429"| TH["Chờ rate-limit-reset giây"]
    X --> I
```

### c) Tự quản lý MFA khi đã đăng nhập

```mermaid
flowchart TD
    A([Cài đặt bảo mật]) --> S["GET /auth/mfa/status"]
    S --> D{"Đã đăng ký?"}
    D -->|Chưa| EN["Hiện QR + ô nhập mã<br/>POST /auth/mfa/enroll"]
    D -->|Rồi| DI["Nút Tắt MFA<br/>(nhập mã xác nhận)<br/>POST /auth/mfa/disable"]
    DI --> C{"Project bắt buộc MFA<br/>và đây là yếu tố cuối?"}
    C -->|Có| BL["Chặn: 'Cannot remove the last MFA factor because MFA is required'"]
    C -->|Không| OFF["MFA đã tắt; secret TOTP bị thay bằng secret ngẫu nhiên"]
    A --> CH["Dùng email làm yếu tố:<br/>POST /auth/mfa/send-email-challenge"]
```

**Mất điện thoại:** người dùng không tự khôi phục; **admin** gọi `members/{id}/mfa/reset` (xem 3.8).

## 3.6 Mật khẩu — F-12, F-13

```mermaid
flowchart TD
    subgraph Doi["Đã đăng nhập: đổi mật khẩu"]
        D1["Nhập mật khẩu cũ + mới"] -->|"POST /auth/changepassword"| D2{"Hợp lệ?"}
        D2 -->|"Sai mật khẩu cũ"| DE1["'Incorrect password' (trường oldPassword)"]
        D2 -->|"Mới có trong dữ liệu rò rỉ"| DE2["'Password found in breach database'"]
        D2 -->|OK| D3["Đổi xong"]
    end
    subgraph Quen["Quên mật khẩu"]
        Q1["Nhập email + reCAPTCHA"] -->|"POST /auth/resetpassword"| Q2["LUÔN báo 'Đã gửi nếu email tồn tại'<br/>(không lộ tài khoản)"]
        Q2 --> Q3["Người dùng mở thư<br/>(link 1 giờ, dùng 1 lần)"]
        Q3 --> Q4["Nhập mật khẩu mới"]
        Q4 -->|"POST /auth/setpassword"| Q5{"Link hợp lệ?"}
        Q5 -->|"Đã dùng / hết hạn / sai secret"| QE["'Already used' / 'Expired' / 'Incorrect secret'"]
        Q5 -->|OK| Q6["Đổi xong, email coi như đã xác minh,<br/>MỌI phiên đang đăng nhập bị đăng xuất"]
    end
```

Tài khoản đăng nhập bằng IdP ngoài **không đặt lại mật khẩu ở đây** (`Cannot reset password for external auth`); người dùng đổi ở IdP của công ty.

## 3.7 Đăng nhập bằng tài khoản công ty / Google — F-05, F-08, F-09

```mermaid
flowchart TD
    A["Nhập email công ty"] -->|"POST /auth/method"| B["Nhận authorizeUrl + cookie auth-external-state"]
    B --> C["Trình duyệt sang trang IdP"]
    C --> D["Người dùng đăng nhập ở IdP"]
    D --> E["IdP chuyển về /auth/external?code&state"]
    E --> F{"Kiểm tra"}
    F -->|"state sai / không đúng trình duyệt"| X1["400 Invalid state"]
    F -->|"Email IdP chưa xác minh"| X2["400 Email address is not verified by the identity provider"]
    F -->|"Email không thuộc miền"| X3["400 Email address does not match domain"]
    F -->|"Không có membership"| X4["400 User not found"]
    F -->|OK| G["302 về ứng dụng kèm code"]
    H["Nút Đăng nhập bằng Google"] -->|"POST /auth/google"| I{"Credential hợp lệ,<br/>email đã xác minh?"}
    I -->|Có| J["Như đăng nhập thường: LoginResult"]
    I -->|"Chưa có tài khoản và createUser"| K["Tạo user rồi đăng nhập"]
    I -->|Không| X5["400 Failed to verify Google credential"]
```

## 3.8 Quản trị tổ chức (A4) — F-23, F-24, F-25

```mermaid
flowchart TD
    A([Admin vào Quản trị]) --> B{"Việc cần làm"}
    B --> P["Cài đặt tổ chức<br/>POST /admin/projects/{id}/settings<br/>(bắt buộc MFA, phương thức MFA cho phép,<br/>miền email bác sĩ, tên ứng dụng)"]
    B --> S["Site Google / reCAPTCHA<br/>POST .../sites (tối đa 20)"]
    B --> C["Tạo ứng dụng client<br/>POST .../client → secret hiện MỘT lần"]
    B --> I["Mời thành viên<br/>POST .../invite"]
    B --> M["Quản lý thành viên<br/>GET/POST/DELETE .../members/{id}"]
    M --> M1["Sửa: admin, active, externalId, accessPolicy"]
    M --> M2["Đặt lại mật khẩu (gửi thư)"]
    M --> M3["Đặt lại MFA (khi mất thiết bị)"]
    M --> M4["Xóa thành viên"]
    I --> I1["Chọn loại hồ sơ, email hoặc externalId,<br/>chính sách, có gửi thư?"]
    I1 --> I2{"Hợp lệ?"}
    I2 -->|"Đã là member / trùng externalId"| I3["409"]
    I2 -->|"Miền email bị chặn"| I4["400"]
    I2 -->|OK| I5["Tạo user + hồ sơ + membership; gửi thư đặt mật khẩu"]
    M4 --> G{"Chủ project / admin cuối cùng?"}
    G -->|Có| GE["400 chặn"]
    G -->|Không| GO["Xóa; token member bị từ chối ngay"]
```

Quy tắc UX cho màn quản trị: thông tin bí mật hiển thị `********` (secret project, secret site); **gửi lại `********` nghĩa là giữ nguyên**; membership của project khác luôn báo 403 chung (không cho dò id).

## 3.9 Ứng dụng đối tác / dịch vụ máy (A5, A6) — F-16..F-21

```mermaid
flowchart TD
    A{"Loại ứng dụng"} -->|"SPA / mobile (công khai)"| P["Authorization code + PKCE<br/>(3.2 rồi POST /oauth2/token)"]
    A -->|"Web có backend"| W["Authorization code + client_secret<br/>hoặc private_key_jwt"]
    A -->|"Dịch vụ máy"| C["client_credentials<br/>(không refresh token, không scope fhir.* / patient/)"]
    A -->|"Backend thay mặt người dùng"| PA["pre-authorized_code (admin cấp trước)"]
    A -->|"Đã có token IdP"| TE["token-exchange"]
    P & W --> T["Nhận access, refresh, id token"]
    T --> R["Hết hạn → refresh (xoay secret)"]
    T --> U["userinfo / introspect"]
    T --> LO["Kết thúc → /oauth2/logout"]
```
