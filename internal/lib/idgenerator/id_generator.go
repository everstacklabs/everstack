package idgenerator

type Generator interface {
	Next() (string, error)
}
