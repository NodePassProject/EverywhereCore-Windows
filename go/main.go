package main

// Required by -buildmode=c-shared: the main package must declare an
// entry point, but a shared library is never executed — the Go
// runtime boots on first exported call.
func main() {}
