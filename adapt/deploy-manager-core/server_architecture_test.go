package main

import (
	"debug/elf"
	"encoding/binary"
	"testing"
)

func Test253ServerArchitectureCatalogue(t *testing.T) {
	for _, tc := range []struct {
		output string
		want   serverArchitecture
	}{
		{"x86_64\n", serverAMD64}, {" AMD64 ", serverAMD64},
		{"aarch64\n", serverARM64}, {" ARM64\r\n", serverARM64},
		{"armv7l", serverARMV7}, {"armv7", serverARMV7}, {"ARMHF", serverARMV7},
		{"", ""}, {"riscv64", ""}, {"armv6l", ""}, {"aarch64\nx86_64", ""},
		{"noise\naarch64", ""}, {"aarch64\x00", ""},
	} {
		got, err := resolveServerArchitecture(tc.output)
		if got != tc.want || (err == nil) != (tc.want != "") {
			t.Fatalf("architecture %q: got %q, %v", tc.output, got, err)
		}
	}
}

func Test253ServerArchitectureAssetTrust(t *testing.T) {
	for _, arch := range []serverArchitecture{serverAMD64, serverARM64, serverARMV7, "invalid"} {
		f, size, digest, err := approvedServerAsset(arch)
		if arch == serverARM64 {
			if err != nil || f.name != "csqtt-core8-linux-arm64" || size != expectedServerSize || digest != expectedServerSHA {
				t.Fatalf("existing ARM64 pin changed")
			}
		} else if err == nil || size != 0 || digest != "" || f.name != "" {
			t.Fatalf("unapproved asset accepted: %q", arch)
		}
	}
}

// Inert minimal ELF headers, not executable server payloads.
func architectureELFHeader(class elf.Class, machine elf.Machine) []byte {
	n := 64
	if class == elf.ELFCLASS32 {
		n = 52
	}
	data := make([]byte, n)
	copy(data, []byte{0x7f, 'E', 'L', 'F', byte(class), byte(elf.ELFDATA2LSB), 1})
	binary.LittleEndian.PutUint16(data[16:], uint16(elf.ET_EXEC))
	binary.LittleEndian.PutUint16(data[18:], uint16(machine))
	binary.LittleEndian.PutUint32(data[20:], 1)
	if n == 64 {
		binary.LittleEndian.PutUint16(data[52:], 64)
	} else {
		binary.LittleEndian.PutUint16(data[40:], 52)
	}
	return data
}

func Test253ServerArchitectureELF(t *testing.T) {
	arches := []serverArchitecture{serverAMD64, serverARM64, serverARMV7}
	for _, arch := range arches {
		format, err := serverFormat(arch)
		if err != nil {
			t.Fatal(err)
		}
		data := architectureELFHeader(format.class, format.machine)
		for _, candidate := range arches {
			if (verifyServerELF(data, candidate) == nil) != (arch == candidate) {
				t.Fatalf("ELF architecture mismatch: %s vs %s", arch, candidate)
			}
		}
		if verifyServerELF(data[:10], arch) == nil {
			t.Fatal("truncated ELF accepted")
		}
	}
	if verifyServerELF(architectureELFHeader(elf.ELFCLASS64, elf.EM_ARM), serverARMV7) == nil {
		t.Fatal("wrong ELF class accepted")
	}
}
