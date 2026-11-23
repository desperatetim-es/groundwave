// SPDX-License-Identifier: GPL-3.0-only
// Copyright (c) 2026 KD9PSM

// Command release builds the Linux release tarballs, on any operating system.
//
//	go run ./tools/release -version 0.1.0          (from the repository folder)
//
// It cross-compiles a fully static binary for each Linux CPU type (CGO off, so
// there is nothing to install on the target) and writes, into dist/:
//
//	groundwave-<version>-linux-x86_64.tar.gz     normal PCs            (uname -m: x86_64)
//	groundwave-<version>-linux-aarch64.tar.gz    64-bit Raspberry Pi   (uname -m: aarch64)
//	groundwave-<version>-linux-armv7l.tar.gz     32-bit Raspberry Pi   (uname -m: armv7l)
//	SHA256SUMS                                   checksums for the files above
//
// Each tarball holds groundwave/ with the program (executable bit set, which a zip
// would lose), LICENSE and a short README.txt. Building the tarballs in Go means the
// executable bit and file ownership are right even when run on Windows.
package main

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	_ "embed"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

//go:embed linux-readme.txt
var linuxReadme string

type target struct {
	goarch string
	goarm  string // only for 32-bit ARM
	uname  string // what `uname -m` prints on that machine
}

var targets = []target{
	{"amd64", "", "x86_64"},
	{"arm64", "", "aarch64"},
	{"arm", "7", "armv7l"},
}

func main() {
	version := flag.String("version", "dev", "version string baked into the program and the file names")
	outDir := flag.String("out", "dist", "output folder")
	flag.Parse()
	if _, err := os.Stat("go.mod"); err != nil {
		log.Fatal("run this from the repository folder (the one that contains go.mod): ", err)
	}
	if err := os.MkdirAll(*outDir, 0o755); err != nil {
		log.Fatal(err)
	}
	goBin := findGo()

	var sums []string
	for _, t := range targets {
		bin, err := build(goBin, t, *version)
		if err != nil {
			log.Fatalf("build %s: %v", t.uname, err)
		}
		name := fmt.Sprintf("groundwave-%s-linux-%s.tar.gz", *version, t.uname)
		path := filepath.Join(*outDir, name)
		if err := pack(path, bin, *version, t); err != nil {
			log.Fatalf("pack %s: %v", name, err)
		}
		os.Remove(bin)
		sum, size, err := checksum(path)
		if err != nil {
			log.Fatal(err)
		}
		sums = append(sums, fmt.Sprintf("%s  %s\n", sum, name))
		fmt.Printf("%-48s %5.1f MB  sha256 %s\n", name, float64(size)/(1<<20), sum)
	}
	if err := os.WriteFile(filepath.Join(*outDir, "SHA256SUMS"), []byte(strings.Join(sums, "")), 0o644); err != nil {
		log.Fatal(err)
	}
}

func findGo() string {
	if p, err := exec.LookPath("go"); err == nil {
		return p
	}
	exe := "go"
	if runtime.GOOS == "windows" {
		exe = "go.exe"
	}
	return filepath.Join(runtime.GOROOT(), "bin", exe)
}

// build compiles one static Linux binary into a temporary file and returns its path.
func build(goBin string, t target, version string) (string, error) {
	tmp, err := os.CreateTemp("", "groundwave-"+t.uname+"-*")
	if err != nil {
		return "", err
	}
	tmp.Close()
	cmd := exec.Command(goBin, "build", "-trimpath",
		"-ldflags", "-s -w -X main.version="+version,
		"-o", tmp.Name(), "./cmd/groundwave")
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS=linux", "GOARCH="+t.goarch, "GOARM="+t.goarm)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	return tmp.Name(), cmd.Run()
}

// pack writes a .tar.gz holding groundwave/{groundwave,LICENSE,THIRD_PARTY_LICENSES.txt,README.txt}.
func pack(path, binary, version string, t target) (err error) {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer func() {
		if cerr := f.Close(); err == nil {
			err = cerr
		}
	}()
	gz, _ := gzip.NewWriterLevel(f, gzip.BestCompression)
	tw := tar.NewWriter(gz)
	now := time.Now().UTC().Truncate(time.Second)

	add := func(name string, mode int64, data []byte) error {
		h := &tar.Header{Name: "groundwave/" + name, Mode: mode, Size: int64(len(data)), ModTime: now,
			Uname: "root", Gname: "root", Typeflag: tar.TypeReg}
		if err := tw.WriteHeader(h); err != nil {
			return err
		}
		_, err := tw.Write(data)
		return err
	}
	if err := tw.WriteHeader(&tar.Header{Name: "groundwave/", Mode: 0o755, ModTime: now,
		Uname: "root", Gname: "root", Typeflag: tar.TypeDir}); err != nil {
		return err
	}
	exe, err := os.ReadFile(binary)
	if err != nil {
		return err
	}
	if err := add("groundwave", 0o755, exe); err != nil {
		return err
	}
	for _, name := range []string{"LICENSE", "THIRD_PARTY_LICENSES.txt"} {
		text, err := os.ReadFile(name)
		if err != nil {
			return err
		}
		if err := add(name, 0o644, text); err != nil {
			return err
		}
	}
	readme := strings.NewReplacer("{{VERSION}}", version, "{{ARCH}}", t.uname).Replace(linuxReadme)
	if err := add("README.txt", 0o644, []byte(readme)); err != nil {
		return err
	}
	if err := tw.Close(); err != nil {
		return err
	}
	return gz.Close()
}

func checksum(path string) (sum string, size int64, err error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	h := sha256.New()
	size, err = io.Copy(h, f)
	return fmt.Sprintf("%x", h.Sum(nil)), size, err
}
