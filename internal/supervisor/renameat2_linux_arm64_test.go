package supervisor

// Index 0 is the only legal slot. Any other arm64 number fails to compile.
var _ = [1]byte{sysRenameat2 - 276: 0}
