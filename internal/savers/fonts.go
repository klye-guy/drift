package savers

import (
	"bytes"
	"os"

	"github.com/hajimehoshi/ebiten/v2/text/v2"
)

func loadFont(path string) (*text.GoTextFaceSource, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return text.NewGoTextFaceSource(bytes.NewReader(b))
}
