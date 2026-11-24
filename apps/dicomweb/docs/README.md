# dicomweb: tài liệu

[architecture.md](architecture.md) · [config.md](config.md) (sinh từ mã) · [api-contract.md](api-contract.md) (bảng route sinh từ `swagger.json`) · [testing.md](testing.md) · [runbook.md](runbook.md). Tổng quan và cách chạy: [../README.md](../README.md). Kế hoạch: [docs/plans/cds-dicom/dicom.md](../../../docs/plans/cds-dicom/dicom.md) và báo cáo spike [d0-spike.md](../../../docs/plans/cds-dicom/d0-spike.md).

**Tài liệu nghiệp vụ (góc nhìn BA kỹ thuật):** [business/](business/README.md): tác nhân, luồng nghiệp vụ và người dùng, danh mục chức năng F-01..F-13, luật BR-xx, kịch bản chấp nhận.

**Hợp đồng API chi tiết từng route (header, request, response từng trường hợp):** [api-contract/common.md](api-contract/common.md) (quy ước chung), [api-contract/reference/](api-contract/reference/README.md) (bảng từng trường, **sinh từ swagger**, có test) và [api-contract/cases/](api-contract/cases/README.md) (từng trường hợp phản hồi, có test phủ route).
