package web

import (
	"encoding/base64"
	"html/template"
	"image/color"
	"net/url"
	"strconv"

	qrcode "github.com/skip2/go-qrcode"
)

// QRDataURI encodes content as a QR code and returns it as a base64 PNG data URI
// ready to drop into an <img src>. ok is false if encoding fails (the caller
// should then just render without the QR). Light modules on a transparent quiet
// zone so the code sits on a dark card rather than a stark white block (scanners
// decode inverted QR fine).
func QRDataURI(content string) (template.URL, bool) {
	qr, err := qrcode.New(content, qrcode.Medium)
	if err != nil {
		return "", false
	}
	qr.BackgroundColor = color.Transparent
	qr.ForegroundColor = color.RGBA{R: 0x8a, G: 0x97, B: 0xa8, A: 0xff}
	png, err := qr.PNG(256)
	if err != nil {
		return "", false
	}
	return template.URL("data:image/png;base64," + base64.StdEncoding.EncodeToString(png)), true //nolint:gosec // G203: fixed data: URI over base64 PNG, no user input
}

// MeshCoreContactURI builds the meshcore:// deep link that adds a contact in the
// MeshCore app. advertType is the MeshCore advert/contact type (1 = chat/person,
// 2 = repeater).
func MeshCoreContactURI(name, publicKeyHex string, advertType int) string {
	q := url.Values{}
	q.Set("name", name)
	q.Set("public_key", publicKeyHex)
	q.Set("type", strconv.Itoa(advertType))
	return "meshcore://contact/add?" + q.Encode()
}
