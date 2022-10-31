package internal

import (
	"bufio"
	"fmt"
	"os"
)

func Contains[T comparable](items *[]T, value *T) bool {
	for _, item := range *items {
		if item == *value {
			return true
		}
	}

	return false
}

func One[T comparable](items *[]T, cond func(*T) bool) *T {
	for _, item := range *items {
		if cond(&item) {
			return &item
		}
	}

	return nil
}

func Count[T comparable](items *[]T, cond func(*T) bool) int {
	var count = 0
	for _, item := range *items {
		if cond(&item) {
			count++
		}
	}

	return count
}

func Any[T comparable](items *[]T, cond func(*T) bool) bool {
	for _, item := range *items {
		if cond(&item) {
			return true
		}
	}

	return false
}

func All[T comparable](items *[]T, cond func(*T) bool) bool {
	for _, item := range *items {
		if !cond(&item) {
			return false
		}
	}

	return true
}

func GetEnv(key string, fallback string) string {
	if value, ok := os.LookupEnv(key); ok {
		return value
	}
	return fallback
}

func AskToContinue() bool {
	scanner := bufio.NewScanner(os.Stdin)
	for {
		fmt.Print("continue? (Y/n) > ")
		scanner.Scan()
		input := scanner.Text()
		switch input {
		case "y", "Y":
			return true
		default:
			return false
		}
	}
}
