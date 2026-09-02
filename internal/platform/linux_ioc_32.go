//go:build linux && (386 || arm)

package platform

// FS_IOC_GETFLAGS / FS_IOC_SETFLAGS encode sizeof(long)=4 in the size field.
const (
	fsIocGetFlags uintptr = 0x80046601
	fsIocSetFlags uintptr = 0x40046602
)
