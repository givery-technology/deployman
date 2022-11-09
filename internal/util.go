package internal

import (
	"bufio"
	"fmt"
	"os"
)

func Contains[T comparable](items []T, value *T) bool {
	for i := range items {
		if items[i] == *value {
			return true
		}
	}
	return false
}

func FirstOrNil[T any](items []T, cond func(*T) bool) *T {
	for i := range items {
		if cond(&items[i]) {
			return &items[i]
		}
	}
	return nil
}

func FirstOrDefault[T any](items []T, cond func(*T) bool) *T {
	for i := range items {
		if cond(&items[i]) {
			return &items[i]
		}
	}
	return new(T)
}

func Filter[T any](items []T, cond func(*T) bool) []T {
	results := make([]T, 0, len(items))
	for i := range items {
		if cond(&items[i]) {
			results = append(results, items[i])
		}
	}
	return results
}

func Map[T, V any](items []T, conv func(i int, t *T) *V) []V {
	values := make([]V, 0, len(items))
	for i := range items {
		values = append(values, *conv(i, &items[i]))
	}
	return values
}

func Count[T any](items []T, cond func(*T) bool) int {
	var count = 0
	for i := range items {
		if cond(&items[i]) {
			count++
		}
	}
	return count
}

func Any[T any](items []T, cond func(*T) bool) bool {
	for i := range items {
		if cond(&items[i]) {
			return true
		}
	}
	return false
}

func All[T any](items []T, cond func(*T) bool) bool {
	for i := range items {
		if !cond(&items[i]) {
			return false
		}
	}
	return true
}

func Delete[T any](items []T, cond func(*T) bool) []T {
	for i := range items {
		if cond(&items[i]) {
			items = items[:i+copy(items[i:], items[i+1:])]
			return items
		}
	}
	return items
}

// FastDelete Fast deletion (order is no longer guaranteed)
// see: https://zenn.dev/mattn/articles/31dfed3c89956d#%E9%A0%86%E3%82%92%E8%80%83%E3%81%88%E3%81%AA%E3%81%84%E5%89%8A%E9%99%A4(%E3%81%8A%E3%81%BE%E3%81%91)
func FastDelete[T any](items []T, cond func(*T) bool) []T {
	for i := range items {
		if cond(&items[i]) {
			pos := len(items) - 1
			items[i] = items[pos]
			items = items[:pos]
			return items
		}
	}
	return items
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
