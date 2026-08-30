package qr

// GF(256) arithmetic using QR's primitive polynomial x^8+x^4+x^3+x^2+1
// (0x11D), via precomputed exponent/log tables — the standard way to do
// fast Galois-field multiplication without per-call polynomial reduction.
var (
	gfExp [512]byte // doubled so gfExp[a+b] never needs a modulo for products
	gfLog [256]byte
)

func init() {
	x := 1
	for i := range 255 {
		gfExp[i] = byte(x)
		gfLog[byte(x)] = byte(i)
		x <<= 1
		if x&0x100 != 0 {
			x ^= 0x11D
		}
	}
	for i := 255; i < 512; i++ {
		gfExp[i] = gfExp[i-255]
	}
}

func gfMul(a, b byte) byte {
	if a == 0 || b == 0 {
		return 0
	}
	return gfExp[int(gfLog[a])+int(gfLog[b])]
}

// rsGeneratorPoly builds the Reed-Solomon generator polynomial of the
// given degree: the product (x - α^0)(x - α^1)...(x - α^(degree-1)) over
// GF(256) (subtraction is XOR in this field, so it's really (x + α^i)).
// Coefficients are ordered highest-degree first; the polynomial is always
// monic (leading coefficient 1), which rsComputeECC relies on.
func rsGeneratorPoly(degree int) []byte {
	poly := []byte{1}
	for i := range degree {
		root := gfExp[i]
		next := make([]byte, len(poly)+1)
		copy(next, poly) // the "poly * x" term: same coefficients, one slot lower degree
		for j, coef := range poly {
			next[j+1] ^= gfMul(coef, root) // the "poly * root" term
		}
		poly = next
	}
	return poly
}

// rsComputeECC returns the error-correction codewords for data using
// polynomial long division by the degree-eccLen generator polynomial —
// the remainder of data(x)*x^eccLen divided by the generator, which is
// exactly what a QR decoder's Reed-Solomon syndrome check expects.
func rsComputeECC(data []byte, eccLen int) []byte {
	gen := rsGeneratorPoly(eccLen)
	msg := make([]byte, len(data)+eccLen)
	copy(msg, data)
	for i := range data {
		factor := msg[i]
		if factor == 0 {
			continue
		}
		for j, gcoef := range gen {
			msg[i+j] ^= gfMul(gcoef, factor)
		}
	}
	return msg[len(data):]
}
