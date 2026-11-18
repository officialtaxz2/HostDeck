package restore

import "errors"

func diskFree(string) (int64, int64, error) {
	return 0, 0, errors.New("unter Windows nicht messbar")
}
