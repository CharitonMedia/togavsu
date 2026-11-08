package supervisor

// Index 0 is the only legal slot. Any other amd64 number fails to compile.
var _ = [1]byte{sysRenameat2 - 316: 0}
