package main

import (
	"bytes"
	"debug/elf"
	"errors"
	"fmt"
	"strings"
)

// Architecture names and aliases follow CSQTTR 2.5.3 DeployOperations.kt.
// The catalogue describes formats, not permission to install a payload.
type serverArchitecture string

const (
	serverAMD64 serverArchitecture = "amd64"
	serverARM64 serverArchitecture = "arm64"
	serverARMV7 serverArchitecture = "armv7"
)

type serverAssetFormat struct {
	name    string
	class   elf.Class
	machine elf.Machine
}

func resolveServerArchitecture(output string) (serverArchitecture, error) {
	// Keep Core's stricter single-record validation: ambiguous SSH output
	// must not be used to choose an executable.
	switch strings.ToLower(strings.TrimSpace(output)) {
	case "x86_64", "amd64":
		return serverAMD64, nil
	case "aarch64", "arm64":
		return serverARM64, nil
	case "armv7l", "armv7", "armhf":
		return serverARMV7, nil
	default:
		return "", errors.New("не удалось однозначно определить архитектуру VPS (AMD64, ARM64, ARMv7)")
	}
}

func serverFormat(arch serverArchitecture) (serverAssetFormat, error) {
	switch arch {
	case serverAMD64:
		return serverAssetFormat{"csqtt-core8-linux-amd64", elf.ELFCLASS64, elf.EM_X86_64}, nil
	case serverARM64:
		return serverAssetFormat{"csqtt-core8-linux-arm64", elf.ELFCLASS64, elf.EM_AARCH64}, nil
	case serverARMV7:
		return serverAssetFormat{"csqtt-core8-linux-armv7", elf.ELFCLASS32, elf.EM_ARM}, nil
	default:
		return serverAssetFormat{}, errors.New("неизвестная архитектура серверного пакета")
	}
}

func verifyServerELF(data []byte, arch serverArchitecture) error {
	format, err := serverFormat(arch)
	if err != nil {
		return err
	}
	f, err := elf.NewFile(bytes.NewReader(data))
	if err != nil {
		return err
	}
	defer f.Close()
	if f.Class != format.class || f.Machine != format.machine || f.Data != elf.ELFDATA2LSB {
		return fmt.Errorf("ELF не соответствует серверной архитектуре %s", arch)
	}
	return nil
}

// An architecture being recognized does not approve a binary. Only the
// existing reviewed ARM64 payload is currently available. Do not infer
// trust from a filename, ELF header, or a locally supplied manifest.
func approvedServerAsset(arch serverArchitecture) (serverAssetFormat, int64, string, error) {
	format, err := serverFormat(arch)
	if err != nil {
		return serverAssetFormat{}, 0, "", err
	}
	if arch != serverARM64 {
		return serverAssetFormat{}, 0, "", fmt.Errorf("для %s пока нет утверждённого серверного пакета", arch)
	}
	return format, expectedServerSize, expectedServerSHA, nil
}
