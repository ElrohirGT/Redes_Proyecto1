package main

type Optional[T any] struct {
	isValid bool
	value   T
}

func NewOpValue[T any](value T) Optional[T] {
	return Optional[T]{
		isValid: true,
		value:   value,
	}
}

func NewOpEmpty[T any]() Optional[T] {
	return Optional[T]{}
}

func (option *Optional[T]) HasValue() bool {
	return option.isValid
}

func (option *Optional[T]) GetValue() T {
	if !option.isValid {
		panic("Invalid access to optional!")
	}

	return option.value
}
