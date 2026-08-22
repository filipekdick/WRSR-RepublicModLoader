package main

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"
)

func readPEMachine(path string) (uint16, bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, false, err
	}
	defer f.Close()
	dos := make([]byte, 64)
	if _, err := f.ReadAt(dos, 0); err != nil {
		return 0, false, err
	}
	if dos[0] != 'M' || dos[1] != 'Z' {
		return 0, false, errors.New("missing MZ header")
	}
	peOff := int64(binary.LittleEndian.Uint32(dos[0x3c:]))
	hdr := make([]byte, 26)
	if _, err := f.ReadAt(hdr, peOff); err != nil {
		return 0, false, err
	}
	if string(hdr[:4]) != "PE\x00\x00" {
		return 0, false, errors.New("missing PE signature")
	}
	machine := binary.LittleEndian.Uint16(hdr[4:6])
	optMagic := make([]byte, 2)
	optOff := peOff + 24
	if _, err := f.ReadAt(optMagic, optOff); err != nil {
		return 0, false, err
	}
	is64 := binary.LittleEndian.Uint16(optMagic) == 0x20b
	return machine, is64, nil
}

func exportRVA(path, exportName string) (uint32, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	if len(data) < 0x100 || string(data[:2]) != "MZ" {
		return 0, errors.New("invalid DLL PE")
	}
	pe := int(binary.LittleEndian.Uint32(data[0x3c:]))
	if pe < 0 || pe+0x108 > len(data) || string(data[pe:pe+4]) != "PE\x00\x00" {
		return 0, errors.New("invalid PE header")
	}
	sections := int(binary.LittleEndian.Uint16(data[pe+6:]))
	optSize := int(binary.LittleEndian.Uint16(data[pe+20:]))
	opt := pe + 24
	if binary.LittleEndian.Uint16(data[opt:]) != 0x20b {
		return 0, errors.New("runtime DLL is not PE32+")
	}
	exportDirRVA := binary.LittleEndian.Uint32(data[opt+112:])
	exportDirSize := binary.LittleEndian.Uint32(data[opt+116:])
	secTable := opt + optSize
	rvaToOff := func(rva uint32) (int, bool) {
		for i := 0; i < sections; i++ {
			s := secTable + i*40
			if s+40 > len(data) {
				break
			}
			va := binary.LittleEndian.Uint32(data[s+12:])
			vs := binary.LittleEndian.Uint32(data[s+8:])
			rawSize := binary.LittleEndian.Uint32(data[s+16:])
			rawOff := binary.LittleEndian.Uint32(data[s+20:])
			span := vs
			if rawSize > span {
				span = rawSize
			}
			if rva >= va && rva < va+span {
				off := int(rawOff + (rva - va))
				return off, off >= 0 && off < len(data)
			}
		}
		return 0, false
	}
	if exportDirRVA == 0 || exportDirSize == 0 {
		return 0, errors.New("DLL has no export directory")
	}
	eoff, ok := rvaToOff(exportDirRVA)
	if !ok || eoff+40 > len(data) {
		return 0, errors.New("invalid export directory")
	}
	numFuncs := binary.LittleEndian.Uint32(data[eoff+20:])
	numNames := binary.LittleEndian.Uint32(data[eoff+24:])
	funcsRVA := binary.LittleEndian.Uint32(data[eoff+28:])
	namesRVA := binary.LittleEndian.Uint32(data[eoff+32:])
	ordsRVA := binary.LittleEndian.Uint32(data[eoff+36:])
	funcsOff, ok1 := rvaToOff(funcsRVA)
	namesOff, ok2 := rvaToOff(namesRVA)
	ordsOff, ok3 := rvaToOff(ordsRVA)
	if !ok1 || !ok2 || !ok3 {
		return 0, errors.New("invalid export arrays")
	}
	for i := uint32(0); i < numNames; i++ {
		no := namesOff + int(i*4)
		oo := ordsOff + int(i*2)
		if no+4 > len(data) || oo+2 > len(data) {
			break
		}
		nameRVA := binary.LittleEndian.Uint32(data[no:])
		nameOff, ok := rvaToOff(nameRVA)
		if !ok {
			continue
		}
		end := nameOff
		for end < len(data) && data[end] != 0 {
			end++
		}
		if string(data[nameOff:end]) != exportName {
			continue
		}
		ord := uint32(binary.LittleEndian.Uint16(data[oo:]))
		if ord >= numFuncs {
			return 0, errors.New("invalid export ordinal")
		}
		fo := funcsOff + int(ord*4)
		if fo+4 > len(data) {
			return 0, errors.New("invalid export function table")
		}
		return binary.LittleEndian.Uint32(data[fo:]), nil
	}
	return 0, errors.New("export not found: " + exportName)
}

type peSectionLayout struct {
	VirtualAddress  uint32
	VirtualSize     uint32
	RawSize         uint32
	RawOffset       uint32
	Characteristics uint32
	Name            string
}

type peLayout struct {
	Data            []byte
	PEOffset        int
	Optional        int
	Is64            bool
	Machine         uint16
	Characteristics uint16
	Sections        []peSectionLayout
	Directories     [16][2]uint32
}

func parsePELayout(path string) (*peLayout, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(data) < 0x100 || string(data[:2]) != "MZ" {
		return nil, errors.New("missing MZ header")
	}
	peOff := int(binary.LittleEndian.Uint32(data[0x3c:]))
	if peOff < 0 || peOff+24 > len(data) || string(data[peOff:peOff+4]) != "PE\x00\x00" {
		return nil, errors.New("invalid PE header")
	}
	sections := int(binary.LittleEndian.Uint16(data[peOff+6:]))
	optSize := int(binary.LittleEndian.Uint16(data[peOff+20:]))
	opt := peOff + 24
	if opt+optSize > len(data) || sections <= 0 || sections > 128 {
		return nil, errors.New("invalid PE optional/section table")
	}
	magic := binary.LittleEndian.Uint16(data[opt:])
	is64 := magic == 0x20b
	if !is64 && magic != 0x10b {
		return nil, errors.New("unsupported PE optional header")
	}
	layout := &peLayout{
		Data: data, PEOffset: peOff, Optional: opt, Is64: is64,
		Machine:         binary.LittleEndian.Uint16(data[peOff+4:]),
		Characteristics: binary.LittleEndian.Uint16(data[peOff+22:]),
	}
	dirBase := opt + 96
	if is64 {
		dirBase = opt + 112
	}
	for i := 0; i < 16; i++ {
		if dirBase+i*8+8 <= opt+optSize {
			layout.Directories[i][0] = binary.LittleEndian.Uint32(data[dirBase+i*8:])
			layout.Directories[i][1] = binary.LittleEndian.Uint32(data[dirBase+i*8+4:])
		}
	}
	secTable := opt + optSize
	for i := 0; i < sections; i++ {
		s := secTable + i*40
		if s+40 > len(data) {
			return nil, errors.New("truncated section table")
		}
		nameEnd := s
		for nameEnd < s+8 && data[nameEnd] != 0 {
			nameEnd++
		}
		layout.Sections = append(layout.Sections, peSectionLayout{
			Name: string(data[s:nameEnd]), VirtualSize: binary.LittleEndian.Uint32(data[s+8:]),
			VirtualAddress: binary.LittleEndian.Uint32(data[s+12:]), RawSize: binary.LittleEndian.Uint32(data[s+16:]),
			RawOffset: binary.LittleEndian.Uint32(data[s+20:]), Characteristics: binary.LittleEndian.Uint32(data[s+36:]),
		})
	}
	return layout, nil
}

type pluginPEValidation struct {
	Valid        bool
	PEReadable   bool
	DeclaredDLL  bool
	Machine      uint16
	Architecture string
	PEFormat     string
	ParserError  string
	Failures     []string
}

func peMachineName(machine uint16) string {
	switch machine {
	case 0x8664:
		return "AMD64 / x86-64"
	case 0x014c:
		return "x86"
	case 0xAA64:
		return "ARM64"
	case 0x01c4:
		return "ARM Thumb-2"
	case 0:
		return "Unable to determine"
	default:
		return fmt.Sprintf("Unknown (0x%04X)", machine)
	}
}

// validatePluginPE is deliberately a narrow admission check. RML is a loader,
// not a binary-quality certification system: only a readable Windows PE image,
// IMAGE_FILE_DLL, AMD64 machine type and PE32+ format are required here.
func validatePluginPE(path string) pluginPEValidation {
	result := pluginPEValidation{Architecture: "Unable to determine", PEFormat: "Unable to determine"}
	layout, err := parsePELayout(path)
	if err != nil {
		result.ParserError = err.Error()
		result.Failures = append(result.Failures, "Invalid Windows PE image")
		return result
	}
	result.PEReadable = true
	result.Machine = layout.Machine
	result.Architecture = peMachineName(layout.Machine)
	if layout.Is64 {
		result.PEFormat = "PE32+"
	} else {
		result.PEFormat = "PE32"
	}
	result.DeclaredDLL = layout.Characteristics&0x2000 != 0 // IMAGE_FILE_DLL
	if !result.DeclaredDLL {
		result.Failures = append(result.Failures, "Detected PE image is not declared as a DLL")
	}
	if layout.Machine != 0x8664 {
		result.Failures = append(result.Failures, "Invalid architecture")
	}
	if !layout.Is64 {
		result.Failures = append(result.Failures, "Invalid PE format; native WRSR plugins require PE32+")
	}
	result.Valid = result.PEReadable && result.DeclaredDLL && layout.Machine == 0x8664 && layout.Is64
	return result
}

func (p *peLayout) rvaToOffset(rva uint32) (int, bool) {
	for _, s := range p.Sections {
		span := s.VirtualSize
		if s.RawSize > span {
			span = s.RawSize
		}
		if rva >= s.VirtualAddress && rva < s.VirtualAddress+span {
			off := uint64(s.RawOffset) + uint64(rva-s.VirtualAddress)
			return int(off), off < uint64(len(p.Data))
		}
	}
	if int(rva) < len(p.Data) {
		return int(rva), true
	}
	return 0, false
}

func readPEBytesAtRVA(path string, rva uint32, count int) ([]byte, error) {
	if count <= 0 || count > 4096 {
		return nil, errors.New("invalid PE read length")
	}
	layout, err := parsePELayout(path)
	if err != nil {
		return nil, err
	}
	off, ok := layout.rvaToOffset(rva)
	if !ok || off < 0 || off+count > len(layout.Data) {
		return nil, errors.New("RVA is not backed by file data")
	}
	out := make([]byte, count)
	copy(out, layout.Data[off:off+count])
	return out, nil
}

func peExportNames(path string) ([]string, error) {
	layout, err := parsePELayout(path)
	if err != nil {
		return nil, err
	}
	rva, size := layout.Directories[0][0], layout.Directories[0][1]
	if rva == 0 || size == 0 {
		return nil, nil
	}
	off, ok := layout.rvaToOffset(rva)
	if !ok || off+40 > len(layout.Data) {
		return nil, errors.New("invalid export directory")
	}
	namesCount := binary.LittleEndian.Uint32(layout.Data[off+24:])
	namesRVA := binary.LittleEndian.Uint32(layout.Data[off+32:])
	namesOff, ok := layout.rvaToOffset(namesRVA)
	if !ok || namesCount > 65536 {
		return nil, errors.New("invalid export name table")
	}
	var names []string
	for i := uint32(0); i < namesCount; i++ {
		entry := namesOff + int(i*4)
		if entry+4 > len(layout.Data) {
			break
		}
		nameRVA := binary.LittleEndian.Uint32(layout.Data[entry:])
		nameOff, ok := layout.rvaToOffset(nameRVA)
		if !ok {
			continue
		}
		end := nameOff
		for end < len(layout.Data) && end-nameOff < 512 && layout.Data[end] != 0 {
			end++
		}
		if end > nameOff && end < len(layout.Data) {
			names = append(names, string(layout.Data[nameOff:end]))
		}
	}
	return names, nil
}
