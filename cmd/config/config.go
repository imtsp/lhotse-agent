package config

type ProxyConfig struct {
	Services []Service   `yaml:"services"`
	Rules    []RouteRule `yaml:"rules"`
}

type Service struct {
	Name     string    `yaml:"name"`
	Hosts    []string  `yaml:"hosts"`
	Clusters []Cluster `yaml:"clusters"`
}

type Endpoint struct {
	Ip   string `yaml:"ip" json:"ip,omitempty"`
	Port string `yaml:"port" json:"port,omitempty"`
}

type SimpleLB string

var (
	ROUND_ROBIN SimpleLB
	LEAST_CONN  SimpleLB
	RANDOM      SimpleLB
	PASSTHROUGH SimpleLB
)

type ConsistentHashLB struct {
	HttpHeaderName         string `yaml:"httpHeaderName"`
	UseSourceIp            bool   `yaml:"useSourceIp"`
	HttpQueryParameterName string `yaml:"httpQueryParameterName"`
	MinimumRingSize        int    `yaml:"minimumRingSize"`
}

type LoadBalancerSettings struct {
	Simple         SimpleLB         `yaml:"simple"`
	ConsistentHash ConsistentHashLB `yaml:"consistentHash"`
}

type TrafficPolicy struct {
	LoadBalancer LoadBalancerSettings `yaml:"loadBalancer"`
}

type Cluster struct {
	Name          string        `yaml:"name"`
	Endpoints     []*Endpoint   `yaml:"endpoints"`
	TrafficPolicy TrafficPolicy `yaml:"trafficPolicy"`
}

type RouteRule struct {
	Name        string    `yaml:"name"`
	ServiceName string    `yaml:"serviceName"`
	HttpRule    HttpRoute `yaml:"httpRule"`
}

type HttpRoute struct {
	Name     string                 `yaml:"name"`
	Match    HttpMatchRequest       `yaml:"match"`
	Route    []HttpRouteDestination `yaml:"route"`
	Redirect HttpRedirect           `yaml:"redirect"`
	Rewrite  HttpRewrite            `yaml:"rewrite"`
	Timeout  int32                  `yaml:"timeout"`
}

type HttpRouteDestination struct {
	Destination Destination `yaml:"destination"`
	Weight      int32       `yaml:"weight"`
}

type HttpRedirect struct {
	Uri          string `yaml:"uri"`
	Authority    string `yaml:"authority"`
	Port         int32  `yaml:"port"`
	Scheme       string `yaml:"scheme"`
	RedirectCode string `yaml:"redirectCode"`
}

type HttpRewrite struct {
	Uri       string `yaml:"uri"`
	Authority string `yaml:"authority"`
}

type HttpMatchRequest struct {
	Name           string                 `yaml:"name"`
	Uri            StringMatch            `yaml:"uri"`
	Scheme         StringMatch            `yaml:"scheme"`
	Method         StringMatch            `yaml:"method"`
	Authority      StringMatch            `yaml:"authority"`
	Headers        map[string]StringMatch `yaml:"headers"`
	Port           int32                  `yaml:"port"`
	SourceLabels   map[string]string      `yaml:"sourceLabels"`
	QueryParams    map[string]StringMatch `yaml:"queryParams"`
	IgnoreUriCase  bool                   `yaml:"ignoreUriCase"`
	WithoutHeaders map[string]StringMatch `yaml:"withoutHeaders"`
}

type StringMatch struct {
	Exact  string `yaml:"exact"`
	Prefix string `yaml:"prefix"`
	Regex  string `yaml:"regex"`
}

type Destination struct {
	Cluster string `yaml:"cluster"`
}
