package tray

import (
	"bytes"
	_ "embed"
	"encoding/binary"
	"runtime"
	"sync"
)

//go:embed logo.png
var embeddedLogo256 []byte

//go:embed logo_64.png
var embeddedLogo64 []byte

var (
	iconOnce sync.Once
	icoBytes []byte
)

// IconBytes 返回当前操作系统托盘所需的图标字节（Windows 为 .ico，macOS/Linux 为 .png）
func IconBytes() []byte {
	iconOnce.Do(func() {
		icoBytes = wrapPNGAsICO(embeddedLogo64, 64, 64)
	})
	if runtime.GOOS == "windows" {
		return icoBytes
	}
	return embeddedLogo64
}

// AppIconPNG 返回内嵌的高清应用 Logo PNG
func AppIconPNG(_ int) []byte {
	return embeddedLogo256
}

func wrapPNGAsICO(pngData []byte, w, h int) []byte {
	var buf bytes.Buffer
	_ = binary.Write(&buf, binary.LittleEndian, uint16(0))
	_ = binary.Write(&buf, binary.LittleEndian, uint16(1))
	_ = binary.Write(&buf, binary.LittleEndian, uint16(1))

	bw := byte(w)
	bh := byte(h)
	if w >= 256 {
		bw = 0
	}
	if h >= 256 {
		bh = 0
	}
	buf.WriteByte(bw)
	buf.WriteByte(bh)
	buf.WriteByte(0)
	buf.WriteByte(0)
	_ = binary.Write(&buf, binary.LittleEndian, uint16(1))
	_ = binary.Write(&buf, binary.LittleEndian, uint16(32))
	_ = binary.Write(&buf, binary.LittleEndian, uint32(len(pngData)))
	_ = binary.Write(&buf, binary.LittleEndian, uint32(6+16))
	buf.Write(pngData)
	return buf.Bytes()
}
