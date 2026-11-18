package restore

import "errors"

func lookupCaddy() (int, int, error) { return 0, 0, errors.New("nur unter Linux") }

func (s *SystemCaddy) Owner([]string, int, int) error { return errors.New("nur unter Linux") }
