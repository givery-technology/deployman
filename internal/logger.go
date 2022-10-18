package internal

import (
	"errors"
	"log"
)

type Logger interface {
	Debug(message string)
	Info(message string)
	Warn(message string, error error)
	Error(message string, error error)
	Fatal(message string, error error)
}

type DefaultLogger struct {
	Verbose bool
}

func (l *DefaultLogger) Debug(message string) {
	if l.Verbose {
		log.Printf("\u001B[36mDEBUG\u001B[0m %s", message)
	}
}

func (l *DefaultLogger) Info(message string) {
	log.Printf("\u001B[34mINFO\u001B[0m %s", message)
}

func (l *DefaultLogger) Warn(message string, error error) {
	if error == nil {
		error = errors.New("")
	}

	if l.Verbose {
		log.Printf("\u001B[33mWARN\u001B[0m %s\n%+v", message, error)
	} else {
		log.Printf("\u001B[33mWARN\u001B[0m %s\n%s", message, error)
	}
}

func (l *DefaultLogger) Error(message string, error error) {
	if error == nil {
		error = errors.New("")
	}

	if l.Verbose {
		log.Printf("\u001B[31mERROR\u001B[0m %s\n%+v", message, error)
	} else {
		log.Printf("\u001B[31mERROR\u001B[0m %s\n%s", message, error)
	}
}

func (l *DefaultLogger) Fatal(message string, error error) {
	if error == nil {
		error = errors.New("")
	}

	log.Fatalf("\u001B[31mFATAL\u001B[0m %s\n%+v", message, error)
}
