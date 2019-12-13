package utils

import "log"

func ReturnError(errs ...error) error {
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}

func LogErrors(errs ...error) error {
	for _, err := range errs {
		if err != nil {
			log.Println(err)
		}
	}
	return nil
}

func HandleErrors(f func(...error) error, errs ...error) error {
	return f(errs...)
}