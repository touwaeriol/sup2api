//go:build !linux

package sandbox

func fillPlatformReport(*report) {}

func platformChild(string) int { return 99 }

func cleanupPlatform() {}
