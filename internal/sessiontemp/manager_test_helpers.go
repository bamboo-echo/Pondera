package sessiontemp

import filelock "pondera/internal/identitylock"

func tryLockForTest(path string) (func(), error) {
	return filelock.Acquire(nilContext(), path)
}
