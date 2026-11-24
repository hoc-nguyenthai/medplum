# Spike D0: thư viện DICOM cho `apps/dicomweb`

Mã dùng một lần để đo (kết quả ở [docs/plans/cds-dicom/d0-spike.md](../../docs/plans/cds-dicom/d0-spike.md)); module Go riêng nên không ảnh hưởng `go.mod` chính.
`scan.go` là bản mẫu của bộ quét luồng sẽ thành `pkg/dicomweb` ở D5; `gen.py` sinh các tệp DICOM tổng hợp (không dữ liệu thật) để làm fixture.

```
python3 gen.py <thư mục ra>          # unc_1frame, unc_multiframe, implicit_le, undefined_sq, jpeg_baseline, jpeg2000, big_120mb, corrupt_*
go build -o spike . && ./spike <full|skippx|unproc|head|headstream|scan|json|stow> <tệp.dcm>
./spike multipart x
```
