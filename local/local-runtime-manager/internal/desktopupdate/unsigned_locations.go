package desktopupdate

// UnsignedApplicationLocations supplies the OS account paths and the system
// Applications directory. Neither is selected by the update invocation.
func UnsignedApplicationLocations() (UnsignedTestPaths, string, error) {
	paths, err := UnsignedPaths()
	return paths, "/Applications", err
}
