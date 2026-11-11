package main

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// executeCLI runs the root command with args and returns everything Cobra
// printed (errors and usage). Cobra keeps state between Execute calls in the
// same process — flag values and their Changed marks, SilenceUsage set by a
// RunE, the output writers — so all of it is reset when the test ends.
func executeCLI(t *testing.T, args ...string) (string, error) {
	t.Helper()
	setGlobals(t, false, 1, 3)
	t.Cleanup(func() {
		rootCmd.SetArgs(nil)
		rootCmd.SetOut(nil)
		rootCmd.SetErr(nil)
		for _, c := range append([]*cobra.Command{rootCmd}, rootCmd.Commands()...) {
			c.SilenceUsage = false
			c.Flags().VisitAll(func(f *pflag.Flag) {
				_ = f.Value.Set(f.DefValue)
				f.Changed = false
			})
		}
	})

	var buf bytes.Buffer
	rootCmd.SetOut(&buf)
	rootCmd.SetErr(&buf)
	rootCmd.SetArgs(args)
	err := rootCmd.Execute()
	return buf.String(), err
}

func TestRequiredFlags(t *testing.T) {
	dir := t.TempDir()
	in := filepath.Join(dir, "in.png")
	msg := filepath.Join(dir, "msg.bin")
	out := filepath.Join(dir, "out.png")
	cases := []struct {
		name    string
		args    []string
		missing string
	}{
		{"encode without -i", []string{"encode", "-f", msg, "-o", out, "-p", testPass}, "input_image"},
		{"encode without -f", []string{"encode", "-i", in, "-o", out, "-p", testPass}, "input_file"},
		{"encode without -o", []string{"encode", "-i", in, "-f", msg, "-p", testPass}, "output_image"},
		{"decode without -i", []string{"decode", "-o", out, "-p", testPass}, "input_image"},
		{"decode without -o", []string{"decode", "-i", in, "-p", testPass}, "output_file"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			output, err := executeCLI(t, tc.args...)
			if err == nil {
				t.Fatal("command succeeded without a required flag")
			}
			if !strings.Contains(err.Error(), `required flag(s) "`+tc.missing+`" not set`) {
				t.Errorf("error does not name the missing flag %q: %v", tc.missing, err)
			}
			if !strings.Contains(output, "Usage:") {
				t.Errorf("usage not printed for a usage error; output:\n%s", output)
			}
			assertOnlyFiles(t, dir)
		})
	}
}

// encodedImage writes a carrier, encodes a payload into it and returns the
// directory, the stego image path and the payload.
func encodedImage(t *testing.T) (string, string, []byte) {
	t.Helper()
	dir := t.TempDir()
	carrier := filepath.Join(dir, "carrier.png")
	writeCarrier(t, carrier, noisyNRGBA(64, 64, 90))
	msg, payload := writePayload(t, dir, 200, 91)
	out := filepath.Join(dir, "stego.png")
	if err := encodeFile(t, carrier, msg, out, false, 1, 3); err != nil {
		t.Fatalf("encode: %v", err)
	}
	return dir, out, payload
}

func runDecodeTo(t *testing.T, img, out, pass string) error {
	t.Helper()
	setGlobals(t, false, 1, 3)
	decoderFlags.inputFile = img
	decoderFlags.outputFile = out
	decoderFlags.key = pass
	return runDecode()
}

func TestDecodeOutputIsPrivate(t *testing.T) {
	dir, img, payload := encodedImage(t)

	t.Run("new file", func(t *testing.T) {
		out := filepath.Join(dir, "secret.bin")
		if err := runDecodeTo(t, img, out, testPass); err != nil {
			t.Fatalf("decode: %v", err)
		}
		fi, err := os.Stat(out)
		if err != nil {
			t.Fatal(err)
		}
		if perm := fi.Mode().Perm(); perm != 0600 {
			t.Errorf("decoded output has mode %#o, want 0600", perm)
		}
		got, _ := os.ReadFile(out)
		if !bytes.Equal(got, payload) {
			t.Error("decoded payload differs")
		}
	})

	t.Run("replaces world-readable file", func(t *testing.T) {
		out := filepath.Join(dir, "existing.bin")
		if err := os.WriteFile(out, []byte("old"), 0644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(out, 0644); err != nil {
			t.Fatal(err)
		}
		if err := runDecodeTo(t, img, out, testPass); err != nil {
			t.Fatalf("decode: %v", err)
		}
		fi, err := os.Stat(out)
		if err != nil {
			t.Fatal(err)
		}
		if perm := fi.Mode().Perm(); perm != 0600 {
			t.Errorf("decoded output has mode %#o, want 0600", perm)
		}
		got, _ := os.ReadFile(out)
		if !bytes.Equal(got, payload) {
			t.Error("decoded payload differs")
		}
	})

	assertOnlyFiles(t, dir, "carrier.png", "payload.bin", "stego.png", "secret.bin", "existing.bin")
}

func TestFailedDecodeWritesNothing(t *testing.T) {
	dir, img, _ := encodedImage(t)

	t.Run("no output file", func(t *testing.T) {
		out := filepath.Join(dir, "secret.bin")
		if err := runDecodeTo(t, img, out, "wrong password"); err == nil {
			t.Fatal("decode succeeded with the wrong password")
		}
		if _, err := os.Stat(out); !os.IsNotExist(err) {
			t.Errorf("output file exists after failed decode (stat err: %v)", err)
		}
	})

	t.Run("existing output preserved", func(t *testing.T) {
		out := filepath.Join(dir, "existing.bin")
		original := []byte("precious existing content")
		if err := os.WriteFile(out, original, 0600); err != nil {
			t.Fatal(err)
		}
		if err := runDecodeTo(t, img, out, "wrong password"); err == nil {
			t.Fatal("decode succeeded with the wrong password")
		}
		got, err := os.ReadFile(out)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, original) {
			t.Error("existing output was modified by a failed decode")
		}
	})

	assertOnlyFiles(t, dir, "carrier.png", "payload.bin", "stego.png", "existing.bin")
}

// TestDecodeWritesThroughLinksAndDevices checks that a symlink or device at
// the output path (as with -o /dev/stdout) is written through in place rather
// than replaced by, or failing, the temp-file rename.
func TestDecodeWritesThroughLinksAndDevices(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no /dev/null or unprivileged symlinks")
	}
	dir, img, payload := encodedImage(t)

	t.Run("device", func(t *testing.T) {
		if err := runDecodeTo(t, img, os.DevNull, testPass); err != nil {
			t.Fatalf("decode to %s: %v", os.DevNull, err)
		}
	})

	t.Run("symlink to regular file", func(t *testing.T) {
		target := filepath.Join(dir, "target.bin")
		if err := os.WriteFile(target, []byte("old content that is longer than nothing"), 0600); err != nil {
			t.Fatal(err)
		}
		link := filepath.Join(dir, "link.bin")
		if err := os.Symlink(target, link); err != nil {
			t.Fatal(err)
		}
		if err := runDecodeTo(t, img, link, testPass); err != nil {
			t.Fatalf("decode: %v", err)
		}
		fi, err := os.Lstat(link)
		if err != nil {
			t.Fatal(err)
		}
		if fi.Mode()&os.ModeSymlink == 0 {
			t.Error("symlink was replaced by a regular file")
		}
		got, _ := os.ReadFile(target)
		if !bytes.Equal(got, payload) {
			t.Error("payload not written through the symlink (or not truncated)")
		}
	})
}

func TestEncodeRejectsUnknownOutputExtension(t *testing.T) {
	t.Setenv("STEG_PASSWORD", "")
	for _, name := range []string{"out.jpg", "out", "out.PNG.gif"} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			msg, _ := writePayload(t, dir, 10, 92)
			out := filepath.Join(dir, name)
			// The carrier does not exist and no password is available: the
			// extension error must come first, before any file or prompt.
			_, err := executeCLI(t, "encode", "-i", filepath.Join(dir, "missing.png"), "-f", msg, "-o", out)
			if err == nil {
				t.Fatal("encode accepted an unknown output extension")
			}
			for _, want := range []string{"unsupported output image extension", ".png, .bmp, .tif, .tiff"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q does not contain %q", err, want)
				}
			}
			assertOnlyFiles(t, dir, "payload.bin")
		})
	}

	t.Run("upper-case extension accepted", func(t *testing.T) {
		if err := checkOutputExt("OUT.TIFF"); err != nil {
			t.Errorf("checkOutputExt(OUT.TIFF): %v", err)
		}
	})
}

func TestRuntimeErrorDoesNotPrintUsage(t *testing.T) {
	_, img, _ := encodedImage(t)
	cases := []struct {
		name string
		args func(dir string) []string
	}{
		{"decode with wrong password", func(dir string) []string {
			return []string{"decode", "-i", img, "-o", filepath.Join(dir, "x.bin"), "-p", "wrong password"}
		}},
		{"encode with unreadable carrier", func(dir string) []string {
			return []string{"encode", "-i", filepath.Join(dir, "missing.png"), "-f", img, "-o", filepath.Join(dir, "o.png"), "-p", testPass}
		}},
		{"capacity with unreadable image", func(dir string) []string {
			return []string{"capacity", "-i", filepath.Join(dir, "missing.png")}
		}},
		{"detect with unreadable image", func(dir string) []string {
			return []string{"detect", "-i", filepath.Join(dir, "missing.png")}
		}},
		{"test-visual with unreadable image", func(dir string) []string {
			return []string{"test-visual", "-i", filepath.Join(dir, "missing.png"), "-o", filepath.Join(dir, "v"), "-p", testPass}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			output, err := executeCLI(t, tc.args(t.TempDir())...)
			if err == nil {
				t.Fatal("command succeeded unexpectedly")
			}
			if !strings.Contains(output, "Error: "+err.Error()) {
				t.Errorf("error not reported; output:\n%s", output)
			}
			if strings.Contains(output, "Usage:") {
				t.Errorf("usage printed for a runtime error; output:\n%s", output)
			}
		})
	}

	// A later usage error must still print usage: SilenceUsage set by an
	// earlier run must not leak.
	output, err := executeCLI(t, "decode", "-i", img, "-p", testPass)
	if err == nil || !strings.Contains(output, "Usage:") {
		t.Errorf("usage error after runtime error: err=%v output:\n%s", err, output)
	}
}
