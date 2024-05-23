package main

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"log"
	"math"
	"os"
)

// const gopher = `iVBORw0KGgoAAAANSUhEUgAAAEsAAAA8CAAAAAALAhhPAAAFfUlEQVRYw62XeWwUVRzHf2+OPbo9d7tsWyiyaZti6eWGAhISoIGKECEKCAiJJkYTiUgTMYSIosYYBBIUIxoSPIINEBDi2VhwkQrVsj1ESgu9doHWdrul7ba73WNm3vOPtsseM9MdwvvrzTs+8/t95ze/33sI5BqiabU6m9En8oNjduLnAEDLUsQXFF8tQ5oxK3vmnNmDSMtrncks9Hhtt/qeWZapHb1ha3UqYSWVl2ZmpWgaXMXGohQAvmeop3bjTRtv6SgaK/Pb9/bFzUrYslbFAmHPp+3WhAYdr+7GN/YnpN46Opv55VDsJkoEpMrY/vO2BIYQ6LLvm0ThY3MzDzzeSJeeWNyTkgnIE5ePKsvKlcg/0T9QMzXalwXMlj54z4c0rh/mzEfr+FgWEz2w6uk8dkzFAgcARAgNp1ZYef8bH2AgvuStbc2/i6CiWGj98y2tw2l4FAXKkQBIf+exyRnteY83LfEwDQAYCoK+P6bxkZm/0966LxcAAILHB56kgD95PPxltuYcMtFTWw/FKkY/6Opf3GGd9ZF+Qp6mzJxzuRSractOmJrH1u8XTvWFHINNkLQLMR+XHXvfPPHw967raE1xxwtA36IMRfkAAG29/7mLuQcb2WOnsJReZGfpiHsSBX81cvMKywYZHhX5hFPtOqPGWZCXnhWGAu6lX91ElKXSalcLXu3UaOXVay57ZSe5f6Gpx7J2MXAsi7EqSp09b/MirKSyJfnfEEgeDjl8FgDAfvewP03zZ+AJ0m9aFRM8eEHBDRKjfcreDXnZdQuAxXpT2NRJ7xl3UkLBhuVGU16gZiGOgZmrSbRdqkILuL/yYoSXHHkl9KXgqNu3PB8oRg0geC5vFmLjad6mUyTKLmF3OtraWDIfACyXqmephaDABawfpi6tqqBZytfQMqOz6S09iWXhktrRaB8Xz4Yi/8gyABDm5NVe6qq/3VzPrcjELWrebVuyY2T7ar4zQyybUCtsQ5Es1FGaZVrRVQwAgHGW2ZCRZshI5bGQi7HesyE972pOSeMM0dSktlzxRdrlqb3Osa6CCS8IJoQQQgBAbTAa5l5epO34rJszibJI8rxLfGzcp1dRosutGeb2VDNgqYrwTiPNsLxXiPi3dz7LiS1WBRBDBOnqEjyy3aQb+/bLiJzz9dIkscVBBLxMfSEac7kO4Fpkngi0ruNBeSOal+u8jgOuqPz12nryMLCniEjtOOOmpt+KEIqsEdocJjYXwrh9OZqWJQyPCTo67LNS/TdxLAv6R5ZNK9npEjbYdT33gRo4o5oTqR34R+OmaSzDBWsAIPhuRcgyoteNi9gF0KzNYWVItPf2TLoXEg+7isNC7uJkgo1iQWOfRSP9NR11RtbZZ3OMG/VhL6jvx+J1m87+RCfJChAtEBQkSBX2PnSiihc/Twh3j0h7qdYQAoRVsRGmq7HU2QRbaxVGa1D6nIOqaIWRjyRZpHMQKWKpZM5feA+lzC4ZFultV8S6T0mzQGhQohi5I8iw+CsqBSxhFMuwyLgSwbghGb0AiIKkSDmGZVmJSiKihsiyOAUs70UkywooYP0bii9GdH4sfr1UNysd3fUyLLMQN+rsmo3grHl9VNJHbbwxoa47Vw5gupIqrZcjPh9R4Nye3nRDk199V+aetmvVtDRE8/+cbgAAgMIWGb3UA0MGLE9SCbWX670TDy1y98c3D27eppUjsZ6fql3jcd5rUe7+ZIlLNQny3Rd+E5Tct3WVhTM5RBCEdiEK0b6B+/ca2gYU393nFj/n1AygRQxPIUA043M42u85+z2SnssKrPl8Mx76NL3E6eXc3be7OD+H4WHbJkKI8AU8irbITQjZ+0hQcPEgId/Fn/pl9crKH02+5o2b9T/eMx7pKoskYgAAAABJRU5ErkJggg==`

const imgPath = `/mnt/c/Users/pablo/Downloads/pixelolmi.png`

const colorSize = 1

const offLast = 0xfffe
const justLast = 0x0001

type ChangeableImage interface {
	image.Image
	Set(x, y int, c color.Color)
}

type reader struct {
	img    image.Image
	cursor int
}

func (t *reader) readByte() (byte, error) {
	var nBits = 8
	var res uint8
	for i := 0; i < nBits; i++ {
		x := int(math.Mod(float64(t.cursor), float64(t.img.Bounds().Max.X)))
		y := int(math.Floor((float64(t.cursor) / float64(t.img.Bounds().Max.X))))
		t.cursor++
		r, _, _, _ := t.img.At(x, y).RGBA()

		bit := r & justLast
		res |= uint8(bit << (nBits - i - 1))

	}

	return res, nil
}

func (t *reader) Read() ([]byte, error) {
	payloadSize := make([]byte, 4)
	for i := 0; i < 4; i++ {
		b, err := t.readByte()
		if err != nil {
			return nil, fmt.Errorf("unable to read payload size %w", err)
		}
		payloadSize[i] = b
	}

	// binary.LittleEndian.PutUint32(bs, uint32(len(payload)))
	payload := make([]byte, binary.LittleEndian.Uint32(payloadSize))
	for i := 0; i < len(payload); i++ {
		b, err := t.readByte()
		if err != nil {
			return nil, fmt.Errorf("unable to read payload %w", err)
		}
		payload[i] = b
	}

	return payload, nil
}

// gopherPNG creates an io.Reader by decoding the base64 encoded image data string in the gopher constant.
// func gopherPNG() io.Reader { return base64.NewDecoder(base64.StdEncoding, strings.NewReader(gopher)) }

type writer struct {
	img    ChangeableImage
	cursor int
}

func newWriter(img ChangeableImage) *writer {
	return &writer{
		img:    img,
		cursor: 0,
	}
}

func byteToBits(b byte) []int {
	var bits []int
	for i := 7; i >= 0; i-- { // Extract bits from most significant to least significant
		bit := (b >> i) & 1
		bits = append(bits, int(bit))
	}

	return bits
}

func (w *writer) writeByte(p byte) error {
	bits := byteToBits(p)
	for _, bit := range bits {
		x := int(math.Mod(float64(w.cursor), float64(w.img.Bounds().Max.X)))
		y := int(math.Floor((float64(w.cursor) / float64(w.img.Bounds().Max.X))))
		w.cursor++
		c := w.img.At(x, y)
		r, g, b, a := c.RGBA()

		if bit == 1 {
			r = r | justLast
		} else {
			r = r & offLast
		}

		w.img.Set(x, y, color.RGBA{uint8(r), uint8(g), uint8(b), uint8(a)})
	}

	return nil
}

func (w *writer) Write(payload []byte) error {
	for _, b := range payload {
		w.writeByte(b)
	}
	return nil
}

func decode(img image.Image, key []byte) ([]byte, error) {
	r := reader{img: img}
	payload, err := r.Read()

	return payload, err
}

func encode(m ChangeableImage, _, payload []byte) (image.Image, error) {
	b := m.Bounds()
	points := ((b.Max.Y - b.Min.Y) * (b.Max.X - b.Min.X))
	if len(payload) > points*colorSize {
		return m, fmt.Errorf("payload is too large")
	}

	payloadLength := float64(4 * 8)

	bitsPerPoint := int(math.Ceil((float64(len(payload)*8) + payloadLength) / float64(points)))

	if bitsPerPoint > 3 {
		return m, fmt.Errorf("payload is too large")
	}

	w := newWriter(m)

	bs := make([]byte, 4)
	binary.LittleEndian.PutUint32(bs, uint32(len(payload)))

	err := w.Write(bs)
	if err != nil {
		log.Fatal(err)
	}

	if err = w.Write(payload); err != nil {
		return nil, err
	}

	return m, nil
}

func main() {
	f, err := os.Open(imgPath)
	if err != nil {
		log.Fatal(err)
	}

	img, err := png.Decode(bufio.NewReader(f))
	if err != nil {
		log.Fatal(err)
	}

	cimg, ok := img.(ChangeableImage)
	if !ok {
		log.Fatal("image its not changeable")
	}

	out, err := os.CreateTemp("", "test")
	if err != nil {
		log.Fatal(err)
	}
	defer out.Close()

	img, err = encode(cimg, []byte("SECRET key"), []byte("SECRET MESSAGE"))
	if err != nil {
		log.Fatal(err)
	}

	err = png.Encode(out, img)
	if err != nil {
		log.Fatal(err)
	}

	msg, err := decode(img, []byte("SECRET key"))
	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf("%v", string(msg))

}
