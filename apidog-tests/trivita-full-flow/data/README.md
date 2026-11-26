# Test data của trivita-full-flow

- `full-flow-e2e`, `full-flow-negative-no-token`, `full-flow-cross-service`: MỘT dòng mỗi tệp. Đây là luồng một mạch, không có trường ràng buộc để chia lớp tương đương; dữ liệu động (UID, PatientID, topic) sinh bằng script ở bước token mỗi lần chạy.
- `full-flow-negative-bad-token`: 6 dòng (Negative 3: chuỗi rác, 3 đoạn, chữ Bearer; Security 2: alg=none, SQLi; Boundary 1: 4096 ký tự), mọi dòng kỳ vọng 401 ở cả 4 service. Token xấu dựng bằng script từ cột `bad_kind` (không để chuỗi giống JWT trong tệp).
- Không có dòng Positive ngoài 3 scenario một dòng; không có dòng Boundary ở các scenario còn lại vì không có tham số đầu vào.
- Không có dòng Boundary/Positive riêng cho từng service: các scenario theo module (trivita-auth, trivita-fhir-server, trivita-dicomweb, trivita-ws-gateway) đã phủ.
- Không chứa bí mật: `clientId`/`clientSecret` chỉ truyền lúc chạy (`--env-var`). Dữ liệu tạo ra mang tiền tố `QA-FF-`/`FullFlowQa`/`qa-ff-` để dễ dọn.
- Chỉ có thể import hàng vào app bằng tay (CLI không upload được); chạy bằng CLI dùng thẳng các tệp này (`run_scenarios.py`).
