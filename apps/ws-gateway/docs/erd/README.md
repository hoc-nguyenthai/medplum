# ws-gateway: ERD và từ điển dữ liệu

`ws-gateway` **không có cơ sở dữ liệu SQL** (không schema, bảng, migration, role riêng); dữ liệu duy nhất là Redis và bộ nhớ tiến trình. Vì vậy bộ này chỉ có một tệp:

| Tệp | Nội dung |
|---|---|
| [04-kho-khac.md](04-kho-khac.md) | Khóa và kênh Redis (Subscription, sổ hiện diện, FHIRcast), TTL, ai ghi và đọc, độ bền, bộ nhớ tiến trình, ACL |

Không có bảng `fhir_*` ở đây nên không áp dụng [docs/erd/fhirstore-physical.md](../../../../docs/erd/fhirstore-physical.md). Dữ liệu `Subscription` mà gateway chuyển tiếp là resource của `fhir-server` (xem [ERD fhir-server](../../../fhir-server/docs/erd/)); gateway không đọc cơ sở dữ liệu.
