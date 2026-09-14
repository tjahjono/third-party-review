package auth

import (
	"fmt"
	"strings"

	"rsc.io/qr"
)

// QRCodeSVG renders text as a self-contained SVG QR code.
//
// SVG rather than a PNG data URI because it is markup the page already trusts,
// scales cleanly, and needs no image-src exception in the Content-Security
// Policy. scale is the size of one module in SVG user units.
//
// Enrolment never depends on this succeeding: the secret is always shown in
// typed form alongside the code, so a render failure degrades to manual entry
// rather than blocking the user.
func QRCodeSVG(text string, scale int) (string, error) {
	if scale < 1 {
		scale = 4
	}
	// Medium error correction: enough redundancy for a phone camera at a
	// screen, without inflating the code.
	code, err := qr.Encode(text, qr.M)
	if err != nil {
		return "", fmt.Errorf("auth: encode QR: %w", err)
	}

	const quiet = 4 // modules of mandatory quiet zone around the symbol
	size := code.Size
	dim := (size + quiet*2) * scale

	var b strings.Builder
	fmt.Fprintf(&b,
		`<svg xmlns="http://www.w3.org/2000/svg" width="%d" height="%d" viewBox="0 0 %d %d" role="img" aria-label="Authenticator enrolment QR code" shape-rendering="crispEdges">`,
		dim, dim, dim, dim)
	fmt.Fprintf(&b, `<rect width="%d" height="%d" fill="#ffffff"/>`, dim, dim)

	// Emit one rect per run of dark modules in a row rather than one per
	// module: a 33x33 symbol is ~1000 rects otherwise, and runs cut that by
	// roughly half with identical output.
	b.WriteString(`<g fill="#000000">`)
	for y := 0; y < size; y++ {
		x := 0
		for x < size {
			if !code.Black(x, y) {
				x++
				continue
			}
			run := 1
			for x+run < size && code.Black(x+run, y) {
				run++
			}
			fmt.Fprintf(&b, `<rect x="%d" y="%d" width="%d" height="%d"/>`,
				(x+quiet)*scale, (y+quiet)*scale, run*scale, scale)
			x += run
		}
	}
	b.WriteString(`</g></svg>`)
	return b.String(), nil
}
