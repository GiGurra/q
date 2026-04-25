package cfg

type Service struct {
	Name string
	Port int
}

func DefaultService() Service {
	return Service{Name: "svc", Port: 9000}
}
