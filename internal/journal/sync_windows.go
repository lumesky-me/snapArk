package journal

// Windows has no supported Go directory-flush primitive. Files and the archive
// manifest are flushed individually; power-loss durability is filesystem dependent.
func syncDirectory(string) error { return nil }
