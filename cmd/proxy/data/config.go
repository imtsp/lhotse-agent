package data

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/creasty/defaults"
	"gopkg.in/yaml.v2"
	"io/ioutil"
	"lhotse-agent/cmd/config"
	proxyConfig "lhotse-agent/cmd/proxy/config"
	"lhotse-agent/pkg/log"
	"lhotse-agent/pkg/protocol/http"
	"lhotse-agent/util"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

var (
	SAVE_TO_FILE = "SAVE2FILE"
)

type MapsMatch interface {
	GetService(host string) *config.Service
	GetRule(service string) *config.RouteRuleList
	GetServiceRule(host string) *config.RouteRuleList
	GetEndpoints(serviceId string) *[]config.Endpoint
	GetCluster(serviceId string) *[]config.Cluster
}

type Maps struct {
	save       chan string
	ServiceMap map[string]*config.Service             `json:"serviceMap" yaml:"serviceMap"`
	RuleMap    map[string]*config.RouteRuleList       `json:"ruleMap" yaml:"ruleMap"`
	Endpoints  map[string]map[string]*config.Endpoint `json:"endpoints" yaml:"endpoints"`
	Clusters   map[string]map[string]*config.Cluster  `json:"clusters" yaml:"clusters"`
}

type CowMaps struct {
	v atomic.Value
	m sync.Mutex // used only by writers
}

func (cm *CowMaps) copy(dst *Maps, src *Maps) {
	dataSrc, err := json.Marshal(src)
	if err != nil {
		log.Error("拷贝失败, 源对象json序列化失败", err)
		return
	}
	if err = json.Unmarshal(dataSrc, dst); err != nil {
		log.Error("拷贝失败, 反序列化到目标对象失败")
		return
	}
}

func (cm *CowMaps) dup(src *Maps) (dst *Maps) {
	dst = NewMaps()
	cm.copy(dst, src)
	return dst
}

func NewCowMaps(maps *Maps) *CowMaps {
	cowMaps := new(CowMaps)
	cowMaps.v.Store(cowMaps.dup(maps))
	return cowMaps
}

func (cm *CowMaps) Get() *Maps {
	return cm.v.Load().(*Maps)
}

func (cm *CowMaps) Update(callback func(maps *Maps) *Maps) {
	cm.m.Lock()
	defer cm.m.Unlock()

	srcObj := cm.v.Load().(*Maps)
	dstObj := cm.dup(srcObj)
	cm.v.Store(callback(dstObj))
}

func NewMaps() *Maps {
	return &Maps{
		save:       make(chan string, 1),
		ServiceMap: make(map[string]*config.Service),
		RuleMap:    make(map[string]*config.RouteRuleList),
		Endpoints:  map[string]map[string]*config.Endpoint{},
		Clusters:   map[string]map[string]*config.Cluster{},
	}
}

func SaveAsync() {
	ServiceData.Get().save <- SAVE_TO_FILE
}

func (m *Maps) GetService(host string) *config.Service {
	return m.ServiceMap[host]
}

func (m *Maps) GetRule(service string) *config.RouteRuleList {
	return m.RuleMap[service]
}

func (m *Maps) GetServiceRule(host string) *config.RouteRuleList {
	service := m.GetService(host)
	if service == nil {
		return nil
	}
	return m.GetRule(service.Name)
}

func (m *Maps) GetEndpoints(serviceId string) []*config.Endpoint {
	endpointMap := m.Endpoints[serviceId]
	var endpoints []*config.Endpoint
	for _, endpoint := range endpointMap {
		endpoints = append(endpoints, endpoint)
	}
	return endpoints
}

func (m *Maps) GetCluster(serviceId string) []*config.Cluster {
	clustersMap := m.Clusters[serviceId]
	var clusters []*config.Cluster
	for _, cluster := range clustersMap {
		clusters = append(clusters, cluster)
	}
	return clusters
}

func (m *Maps) SaveCache(cacheFile string) {
	util.GO(func() {
		for {
			select {
			case save := <-m.save:
				if save == SAVE_TO_FILE {
					log.Infof("Save Config to file.")
					cacheData, err := json.Marshal(m)
					if err != nil {
						log.Error("配置数据Json序列化失败", err)
						return
					}
					err = ioutil.WriteFile(cacheFile, cacheData, 644)
					if err != nil {
						log.Error("缓存配置数据到文件失败", err)
					}
				}
			}
		}
	})
}

func (m *Maps) LoadCache(cacheFile string) {
	file, err := ioutil.ReadFile(cacheFile)
	if err != nil {
		log.Error("获取缓存数据失败", err)
		return
	}
	if err = json.Unmarshal(file, m); err != nil {
		log.Error("缓存数据json反序列化失败", err)
		return
	}
}

var AutoSaveTimer *time.Timer

func Load(cfg *proxyConfig.Config) {
	defer SaveAsync()
	if cfg.CacheFileName != "" {
		// 加载缓存数据
		ServiceData.Get().LoadCache(cfg.CacheFileName)
	}
	// 加载静态配置数据
	ServiceData.Get().LoadServiceData(cfg.FileName)
	// 保存缓存
	ServiceData.Get().SaveCache(cfg.CacheFileName)
	defer util.GO(func() {
		if AutoSaveTimer == nil {
			AutoSaveTimer = time.NewTimer(cfg.CacheDuration)
		}
		for {
			if AutoSaveTimer == nil {
				break
			}
			select {
			case <-AutoSaveTimer.C:
				SaveAsync()
			}
		}
	})

}

func (m *Maps) LoadServiceData(file string) {
	buf, err := ioutil.ReadFile(file)
	if err != nil {
		log.Error(err)
		return
	}
	var pc = &config.ProxyConfig{}
	err = yaml.Unmarshal(buf, pc)
	if err != nil {
		log.Error(err)
		return
	}
	defaults.Set(pc)
	if err != nil {
		log.Error(err)
		return
	}
	for _, service := range pc.Services {
		if service.LB == nil {
			var balancer config.LoadBalancer = &config.RoundRobinLoadBalancer{}
			service.LB = &balancer
		}
		clusterMap, ok := m.Clusters[service.Name]
		if !ok {
			clusterMap = make(map[string]*config.Cluster, 0)
			m.Clusters[service.Name] = clusterMap
		}
		endpointMap, ok := m.Endpoints[service.Name]
		if !ok {
			endpointMap = make(map[string]*config.Endpoint, 0)
			m.Endpoints[service.Name] = endpointMap
		}
		for i, _ := range service.Clusters {
			var cluster = service.Clusters[i]
			if cluster.TrafficPolicy.LoadBalancer.LoadBalancer == nil {
				var lb config.LoadBalancer = &config.RoundRobinLoadBalancer{}
				switch cluster.TrafficPolicy.LoadBalancer.Simple {
				case config.ROUND_ROBIN:
					lb = &config.RoundRobinLoadBalancer{}
					cluster.TrafficPolicy.LoadBalancer.LoadBalancer = &lb
				case config.RANDOM:
					lb = &config.RandomLoadBalancer{}
					cluster.TrafficPolicy.LoadBalancer.LoadBalancer = &lb
				case config.LEAST_CONN:
					lb = &config.LastConnLoadBalancer{Last: -1}
					cluster.TrafficPolicy.LoadBalancer.LoadBalancer = &lb
				case config.PASSTHROUGH:
					lb = &config.PassThroughLoadBalancer{}
					cluster.TrafficPolicy.LoadBalancer.LoadBalancer = &lb
				default:
					cluster.TrafficPolicy.LoadBalancer.LoadBalancer = &lb
				}
			}

			clusterMap[cluster.Name] = &cluster
			m.Clusters[service.Name] = clusterMap
			for _, endpoint := range cluster.Endpoints {
				endpointMap[fmt.Sprintf("%s:%s", endpoint.Ip, endpoint.Port)] = endpoint
				m.Endpoints[service.Name] = endpointMap
			}
		}
		for _, host := range service.Hosts {
			m.ServiceMap[host] = &service
		}

		for _, rule := range service.Rules {
			rules, ok := m.RuleMap[service.Name]
			if !ok {
				rules = &config.RouteRuleList{}
			}

			rs := append(*rules, rule)
			rules = &rs
			m.RuleMap[service.Name] = rules

			// 初始化负载均衡
			for i := range rule.Http {
				httpRoute := rule.Http[i]
				if httpRoute.LoadBalancer == nil {
					routeDest := httpRoute.Route
					httpRoute.LoadBalancer = &config.WeightRoundRobinBalancer{}
					if routeDest != nil && len(routeDest) > 0 {
						for _, destination := range routeDest {
							if destination.Weight <= 0 {
								continue
							}
							if destination.Destination.Cluster == "" {
								continue
							}
							httpRoute.LoadBalancer.Add(destination.Destination.Cluster, strconv.Itoa(int(destination.Weight)))
						}
					}
				}
			}

		}
	}
}

var ServiceData = NewCowMaps(NewMaps())

func Match(req *http.Request) (*config.Endpoint, error) {
	service := ServiceData.Get().GetService(req.Host)
	if service == nil {
		return nil, errors.New("no service")
	}
	rules := ServiceData.Get().GetRule(service.Name)
	if rules == nil || len(*rules) <= 0 {
		endpoints := ServiceData.Get().GetEndpoints(service.Name)
		if len(endpoints) <= 0 {
			return nil, errors.New("no cluster")
		}
		//
		balancer := *service.LB
		endpoint := balancer.Select(endpoints)
		return endpoint, nil
	} else {
		for _, rule := range *rules {
			for i := range rule.Http {
				httpRule := rule.Http[i]
				if httpRule == nil {
					continue
				}

				if httpRule.Match != nil && len(httpRule.Match) > 0 {
					for _, match := range httpRule.Match {

						// 规则匹配
						methodMatched, methodNeedMatch := checkMatch(match.Method, req.Method, false)
						// 配置了规则
						if methodNeedMatch && !methodMatched {
							// 规则不匹配
							continue
						}

						authorityMatched, authorityNeedMatch := checkMatch(match.Authority, req.Authority, false)
						// 配置了规则
						if authorityNeedMatch && !authorityMatched {
							// 规则不匹配
							continue
						}

						schemeMatched, schemeNeedMatch := checkMatch(match.Scheme, req.URL.Scheme, false)
						// 配置Schema规则
						if schemeNeedMatch && !schemeMatched {
							// 规则不匹配
							continue
						}

						uriMatched, uriNeedMatch := checkMatch(match.Uri, req.RequestURI, match.IgnoreUriCase)
						// 配置了Uri规则
						if uriNeedMatch && !uriMatched {
							// Uri规则不匹配
							continue
						}

						var headerMatched = true
						var headerNeedMatch = len(match.Headers) > 0
						for header, headerMatch := range match.Headers {
							value := req.Header.Get(header)
							matched, needMatch := checkMatch(headerMatch, value, false)
							headerMatched = matched
							if needMatch {
								headerNeedMatch = needMatch
								if !matched {
									headerMatched = false
									break
								}
							}
						}
						if headerNeedMatch && !headerMatched {
							continue
						}

						var headerWithOutMatched = false
						var headerWithOutNeedMatch = len(match.WithoutHeaders) > 0
						for header, headerWithOutMatch := range match.WithoutHeaders {
							value := req.Header.Get(header)
							matched, needMatch := checkMatch(headerWithOutMatch, value, false)
							if needMatch {
								headerWithOutNeedMatch = needMatch
								if matched {
									headerWithOutMatched = true
									break
								}
							}
						}
						if headerWithOutNeedMatch && headerWithOutMatched {
							continue
						}

						var paramMatched = true
						var paramNeedMatch = len(match.QueryParams) > 0
						for paramName, paramMatch := range match.QueryParams {
							values := req.URL.Query().Get(paramName)
							matched, needMatch := checkMatch(paramMatch, values, false)
							if needMatch {
								paramNeedMatch = needMatch
								if matched {
									paramMatched = false
									break
								}
							}
						}
						if paramNeedMatch && !paramMatched {
							continue
						}

						var portMatched = true
						var portNeedMatch = match.Port != 0
						if match.Port != 0 {
							portMatched = match.Port == req.Port
						}
						if portNeedMatch && !portMatched {
							continue
						}

						// 动作提取
						if httpRule.LoadBalancer != nil {
							cluster, err := httpRule.LoadBalancer.Select("")
							if err != nil {
								return nil, err
							}
							clusterMap, ok := ServiceData.Get().Clusters[service.Name]
							if !ok {
								return nil, errors.New("service no cluster")
							}
							clusterV, ok := clusterMap[cluster]
							if !ok || clusterV == nil || clusterV.TrafficPolicy.LoadBalancer.LoadBalancer == nil {
								return nil, errors.New("cluster not found")
							}
							if clusterV.TrafficPolicy.LoadBalancer.Simple == config.PASSTHROUGH {
								return nil, errors.New("PassThrough")
							}
							balancer := *clusterV.TrafficPolicy.LoadBalancer.LoadBalancer
							endpoint := balancer.Select(clusterV.Endpoints)
							return endpoint, nil
						}

					}

				}
			}

		}
	}
	return nil, errors.New("no endpoint")
}

func checkMatch(stringMatch config.StringMatch, reqV string, ignoreCase bool) (isMatched bool, needMatch bool) {
	if !stringMatch.Empty() {
		var tmpReqVal = reqV
		if ignoreCase {
			tmpReqVal = strings.ToLower(reqV)
		}
		if stringMatch.Exact != "" {
			var tmpExact = stringMatch.Exact
			if ignoreCase {
				tmpExact = strings.ToLower(stringMatch.Exact)
			}
			isMatched = tmpReqVal == tmpExact
		} else if stringMatch.Prefix != "" {
			var tmpPrefix = stringMatch.Prefix
			if ignoreCase {
				tmpPrefix = strings.ToLower(stringMatch.Prefix)
			}
			isMatched = strings.HasPrefix(tmpReqVal, tmpPrefix)
		} else if stringMatch.Regex != "" {
			matchString, err := regexp.MatchString(stringMatch.Regex, tmpReqVal)
			isMatched = err == nil && matchString
		}
		needMatch = true
	}
	return isMatched, needMatch
}
