package cli

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"
)

func TestPrintJSONKeepsPairingURISeparators(t *testing.T) {
	original := os.Stdout
	read, write, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = write
	err = printJSON(map[string]string{"uri": "waw://pair?v=1&payload=test"})
	_ = write.Close()
	os.Stdout = original
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(read)
	_ = read.Close()
	if err != nil {
		t.Fatal(err)
	}
	if !json.Valid(data) || !bytes.Contains(data, []byte("waw://pair?v=1&payload=test")) {
		t.Fatalf("pairing URI was not emitted as usable JSON: %s", data)
	}
	if strings.Contains(string(data), `\u0026`) {
		t.Fatalf("pairing URI still contains JSON escaped ampersand: %s", data)
	}
}
