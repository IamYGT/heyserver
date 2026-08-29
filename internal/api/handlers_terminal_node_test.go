package api

import (
	"bytes"
	"encoding/base64"
	"testing"
)

func TestTerminalCommandAllowsOnlyManagedTargets(t *testing.T) {
	local, target, err := terminalCommand("local")
	if err != nil || local == nil || target != "local" {
		t.Fatalf("local command target=%q err=%v", target, err)
	}
	if _, _, err := terminalCommand("managed-node"); err == nil {
		t.Fatal("managed node bypassed the agent relay")
	}
}

func TestTerminalOutputMessagePreservesPTYControlBytes(t *testing.T) {
	raw := []byte{0x1b, 'P', '$', 'f', '{', '}', 0x9c}
	message := terminalOutputMessage(raw, true)
	if message.Type != "output" || message.Encoding != "base64" {
		t.Fatalf("unexpected output envelope: %#v", message)
	}
	decoded, err := base64.StdEncoding.DecodeString(message.Data)
	if err != nil {
		t.Fatalf("decode terminal output: %v", err)
	}
	if !bytes.Equal(decoded, raw) {
		t.Fatalf("decoded output %v, want %v", decoded, raw)
	}
}

func TestTerminalOutputNormalizerConvertsBareC1WithoutBreakingUTF8(t *testing.T) {
	normalizer := &terminalOutputNormalizer{}
	first := normalizer.Normalize([]byte{0x1b, 'P', '$', 'f', 0x9c, 0xc3})
	second := normalizer.Normalize([]byte{0x9c, 'x'})

	wantFirst := []byte{0x1b, 'P', '$', 'f', 0x1b, '\\', 0xc3}
	if !bytes.Equal(first, wantFirst) {
		t.Fatalf("first normalized chunk %v, want %v", first, wantFirst)
	}
	if !bytes.Equal(second, []byte{0x9c, 'x'}) {
		t.Fatalf("split UTF-8 continuation changed: %v", second)
	}
}

func TestTerminalSizeUsesBrowserDimensionsAndSafeBounds(t *testing.T) {
	tests := []struct {
		name               string
		cols, rows         string
		wantCols, wantRows uint16
	}{
		{name: "browser dimensions", cols: "117", rows: "67", wantCols: 117, wantRows: 67},
		{name: "missing defaults", wantCols: 80, wantRows: 24},
		{name: "invalid defaults", cols: "wide", rows: "tall", wantCols: 80, wantRows: 24},
		{name: "minimum", cols: "1", rows: "1", wantCols: 10, wantRows: 4},
		{name: "maximum", cols: "999", rows: "999", wantCols: 500, wantRows: 200},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cols, rows := terminalSize(test.cols, test.rows)
			if cols != test.wantCols || rows != test.wantRows {
				t.Fatalf("terminalSize(%q, %q) = %dx%d, want %dx%d", test.cols, test.rows, cols, rows, test.wantCols, test.wantRows)
			}
		})
	}
}
