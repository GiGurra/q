package cfg

type Config struct {
	Name    string
	Port    int
	Enabled bool
	Tags    []string
}

type Endpoint struct {
	URL    string
	Method string
}

func DefaultConfig() Config {
	return Config{
		Name:    "service-A",
		Port:    8080,
		Enabled: true,
		Tags:    []string{"prod", "edge"},
	}
}
