//go:build linux && (amd64 || arm64 || riscv64 || s390x || loong64)

package platform

// FS_IOC_GETFLAGS / FS_IOC_SETFLAGS encode sizeof(long)=8 in the size field.
const (
	fsIocGetFlags uintptr = 0x80086601
	fsIocSetFlags uintptr = 0x40086602
)
