// Flat C API over core.go, exported from the c-shared DLL for
// P/Invoke. Conventions, chosen for painless C# marshalling:
//
//   - Functions that can fail return char*: NULL on success, a
//     UTF-8 error message on failure.
//   - Every non-NULL char* the DLL returns is malloc'd on the C heap
//     and owned by the caller — release it with EvcoreFreeString.
//     (C# cannot free Go-allocated memory, and Go must not retain
//     pointers it handed across the boundary.)
//   - Parameters are borrowed: the DLL copies what it needs before
//     returning and never keeps the caller's pointer.
//
// See README.md for the matching [DllImport] declarations.
package main

/*
#include <stdlib.h>
*/
import "C"

import "unsafe"

func cerr(err error) *C.char {
	if err == nil {
		return nil
	}
	return C.CString(err.Error())
}

//export EvcoreVersion
func EvcoreVersion() *C.char {
	return C.CString(Version())
}

//export EvcoreSetResourcesPath
func EvcoreSetResourcesPath(path *C.char) *C.char {
	return cerr(SetResourcesPath(C.GoString(path)))
}

//export EvcoreStartCore
func EvcoreStartCore(coreType, configContent *C.char) *C.char {
	return cerr(StartCore(C.GoString(coreType), C.GoString(configContent)))
}

//export EvcoreSuspend
func EvcoreSuspend() *C.char {
	return cerr(Suspend())
}

//export EvcoreResume
func EvcoreResume() *C.char {
	return cerr(Resume())
}

//export EvcoreStopAll
func EvcoreStopAll() *C.char {
	return cerr(StopAll())
}

//export EvcoreFreeString
func EvcoreFreeString(s *C.char) {
	C.free(unsafe.Pointer(s))
}
