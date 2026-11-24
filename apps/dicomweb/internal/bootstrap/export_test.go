package bootstrap

import "time"

// SetPixelBackoff đặt thời gian chờ giữa các lần thử lại của worker pixelData (test dùng giá trị nhỏ).
func SetPixelBackoff(c *Container, f func(n int) time.Duration) { c.Pixels.backoff = f }

// PixelsEnabled cho biết worker pixelData có được bật không.
func PixelsEnabled(c *Container) bool { return c.Pixels != nil }

// PixelQueueSize là cỡ hàng đợi worker.
const PixelQueueSize = pixelQueueSize
