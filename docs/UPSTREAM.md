# Nguồn upstream của medplum

Ghi nhận ở bước 0 của [migration-plan.md](migration-plan.md), ngày 2026-11-20.

| | |
|---|---|
| Repo | https://github.com/medplum/medplum.git |
| Nhánh | `main` |
| Tag | `v5.2.18` ("Release Version 5.2.18 (#11515)") |
| Commit | `98c2d22d9dc841cedd2a38328844400313c19f1d` |
| Ngày commit | 2026-11-19 |
| Thay đổi cục bộ | không (working tree sạch, 0 file thay đổi) |
| Backup (kèm `.git`) | `../trivita-backups/medplum-v5.2.18-with-git.tgz` |

Vì không có thay đổi cục bộ, mã trong `medplum/` giống hệt tag `v5.2.18`. Có thể dựng lại bất cứ lúc nào bằng `git clone` rồi `git checkout v5.2.18`, nên việc bỏ `.git` lồng không làm mất lịch sử.

Nâng cấp upstream sau này: clone bản mới vào thư mục tạm, so sánh với các thư mục đích rồi áp thay đổi thủ công.

## Bản Medplum dùng để port `apps/auth`

Ngày 2026-11-21, thư mục `/Users/hocnt84/Downloads/medplum` có `package.json` ghi phiên bản **5.2.18**, trùng tag ở trên. Các kế hoạch trong [plans/auth](plans/auth/README.md) đối chiếu với `packages/server/src/{auth,oauth,admin}` của bản này. Chưa so sánh từng file với commit `98c2d22d`; nếu cần chắc chắn là cùng mã nguồn thì kiểm bằng `git` trong thư mục đó.
