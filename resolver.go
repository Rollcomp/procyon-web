package web

import (
	"encoding/json"
	"github.com/codnect/goo"
	"github.com/procyon-projects/procyon-context"
	"github.com/procyon-projects/procyon-core"
	"reflect"
	"strings"
	"sync"
)

type HandlerMethodParameterResolver interface {
	SupportsParameter(parameter HandlerMethodParameter) bool
	ResolveParameter(parameter HandlerMethodParameter, request HttpRequest) (interface{}, error)
}

type HandlerMethodParameterResolvers struct {
	parameterResolverCache map[int]HandlerMethodParameterResolver
	resolvers              []HandlerMethodParameterResolver
	cacheMutex             sync.Mutex
}

func NewHandlerMethodParameterResolvers() *HandlerMethodParameterResolvers {
	return &HandlerMethodParameterResolvers{
		parameterResolverCache: make(map[int]HandlerMethodParameterResolver),
		resolvers:              make([]HandlerMethodParameterResolver, 0),
	}
}

func (parameterResolvers *HandlerMethodParameterResolvers) SupportsParameter(parameter HandlerMethodParameter) bool {
	var cacheResolver HandlerMethodParameterResolver
	parameterResolvers.cacheMutex.Lock()
	cacheResolver = parameterResolvers.parameterResolverCache[parameter.HashCode()]
	parameterResolvers.cacheMutex.Unlock()
	if cacheResolver != nil {
		return true
	}
	resolvers := parameterResolvers.resolvers
	for _, resolver := range resolvers {
		if resolver.SupportsParameter(parameter) {
			parameterResolvers.cacheMutex.Lock()
			parameterResolvers.parameterResolverCache[parameter.HashCode()] = resolver
			parameterResolvers.cacheMutex.Unlock()
			return true
		}
	}
	return false
}

func (parameterResolvers *HandlerMethodParameterResolvers) ResolveParameter(parameter HandlerMethodParameter, request HttpRequest) (interface{}, error) {
	resolver := parameterResolvers.findParameterResolver(parameter)
	if resolver == nil {
		return nil, NewNoHandlerParameterResolverError("Parameter resolver not found")
	}
	return resolver.ResolveParameter(parameter, request)
}

func (parameterResolvers *HandlerMethodParameterResolvers) findParameterResolver(parameter HandlerMethodParameter) HandlerMethodParameterResolver {
	var cacheResolver HandlerMethodParameterResolver
	parameterResolvers.cacheMutex.Lock()
	cacheResolver = parameterResolvers.parameterResolverCache[parameter.HashCode()]
	parameterResolvers.cacheMutex.Unlock()
	if cacheResolver != nil {
		return cacheResolver
	}
	resolvers := parameterResolvers.resolvers
	for _, resolver := range resolvers {
		if resolver.SupportsParameter(parameter) {
			parameterResolvers.cacheMutex.Lock()
			parameterResolvers.parameterResolverCache[parameter.HashCode()] = resolver
			parameterResolvers.cacheMutex.Unlock()
			return resolver
		}
	}
	return nil
}

func (parameterResolvers *HandlerMethodParameterResolvers) AddMethodParameterResolver(resolvers ...HandlerMethodParameterResolver) {
	parameterResolvers.resolvers = append(parameterResolvers.resolvers, resolvers...)
}

type ContextMethodParameterResolver struct {
	contextType goo.Type
}

func NewContextMethodParameterResolver() ContextMethodParameterResolver {
	return ContextMethodParameterResolver{
		goo.GetType((*context.Context)(nil)),
	}
}

func (resolver ContextMethodParameterResolver) SupportsParameter(parameter HandlerMethodParameter) bool {
	parameterType := parameter.GetParameterType()
	if parameterType.Equals(resolver.contextType) {
		return true
	}
	return false
}

func (resolver ContextMethodParameterResolver) ResolveParameter(parameter HandlerMethodParameter, request HttpRequest) (interface{}, error) {
	if request.HasAttribute(ApplicationContextAttribute) {
		return request.GetAttribute(ApplicationContextAttribute).(context.Context), nil
	}
	return nil, nil
}

type RequestMethodParameterResolver struct {
	converterService        core.TypeConverterService
	fieldsCache             map[string][]goo.Field
	requestTagExistCacheMap map[string]bool
	cacheMutex              sync.Mutex
}

func NewRequestMethodParameterResolver(converterService core.TypeConverterService) RequestMethodParameterResolver {
	return RequestMethodParameterResolver{
		converterService:        converterService,
		fieldsCache:             make(map[string][]goo.Field),
		requestTagExistCacheMap: make(map[string]bool),
	}
}

func (resolver RequestMethodParameterResolver) SupportsParameter(parameter HandlerMethodParameter) bool {
	if parameter.GetParameterType().IsStruct() {
		structType := parameter.GetParameterType().ToStructType()
		defer func() {
			resolver.cacheMutex.Unlock()
		}()
		resolver.cacheMutex.Lock()
		existsRequestTag, ok := resolver.requestTagExistCacheMap[structType.GetFullName()]
		if ok {
			return existsRequestTag
		}
		fields := structType.GetFields()
		for _, field := range fields {
			fieldType := field.GetType()
			if !strings.HasPrefix(fieldType.ToStructType().GetName(), "struct {") {
				continue
			}
			tag, err := field.GetTagByName("request")
			if err == nil {
				if "body" == tag.Value || "param" == tag.Value || "path" == tag.Value || "header" == tag.Value {
					existsRequestTag = true
					resolver.fieldsCache[structType.GetFullName()] = append(resolver.fieldsCache[structType.GetFullName()], field)
				}
			}
		}
		resolver.requestTagExistCacheMap[structType.GetFullName()] = existsRequestTag
		if existsRequestTag {
			return true
		}
	}
	return false
}

func (resolver RequestMethodParameterResolver) ResolveParameter(parameter HandlerMethodParameter, request HttpRequest) (interface{}, error) {
	if !resolver.SupportsParameter(parameter) {
		return nil, nil /* todo */
	}

	var fields []goo.Field
	parameterType := parameter.GetParameterType()

	resolver.cacheMutex.Lock()
	fields = resolver.fieldsCache[parameterType.GetFullName()]
	parameterObj := parameterType.ToStructType().NewInstance()
	resolver.cacheMutex.Unlock()

	for _, field := range fields {
		fieldType := field.GetType()
		if !fieldType.IsStruct() {
			continue
		}

		requestTag, _ := field.GetTagByName("request")
		fieldVal := field.GetValue(parameterObj)
		if reflect.ValueOf(fieldVal).Kind() == reflect.Ptr && reflect.ValueOf(fieldVal).IsNil() {
			fieldVal = fieldType.ToStructType().NewInstance()
			field.SetValue(parameterObj, fieldVal)
		}

		if "path" == requestTag.Value {
			resolver.bindPathVariables(fieldVal, fieldType.ToStructType(), request)
		} else if "param" == requestTag.Value {
			resolver.bindQueryParameters(fieldVal, fieldType.ToStructType(), request)
		} else if "body" == requestTag.Value {
			json.NewDecoder(request.GetBody()).Decode(fieldVal)
		} else if "header" == requestTag.Value {
			resolver.bindHeader(fieldVal, fieldType.ToStructType(), request)
		}

	}
	if parameterType.IsPointer() {
		return parameterObj, nil
	}
	return reflect.ValueOf(parameterObj).Elem().Interface(), nil
}

func (resolver RequestMethodParameterResolver) bindQueryParameters(parentInstance interface{}, structType goo.Struct, request HttpRequest) {
	queryParams := request.GetQueryParameters()
	if len(queryParams) > 0 && structType.GetFieldCount() > 0 {
		for _, field := range structType.GetFields() {
			tag, err := resolver.getBindingTag(field)
			if err != nil {
				continue
			}
			if value, ok := queryParams[tag.Value]; ok {
				resolver.bindField(parentInstance, field, value[0])
			}
		}
	}
}

func (resolver RequestMethodParameterResolver) bindPathVariables(parentInstance interface{}, structType goo.Struct, request HttpRequest) {
	pathParams := request.GetAttribute(HandlerMappingUriVariableAttribute).(map[string]string)
	if len(pathParams) > 0 && structType.GetFieldCount() > 0 {
		for _, field := range structType.GetFields() {
			tag, err := resolver.getBindingTag(field)
			if err != nil {
				continue
			}
			if value, ok := pathParams[tag.Value]; ok {
				resolver.bindField(parentInstance, field, value)
			}
		}
	}
}

func (resolver RequestMethodParameterResolver) bindHeader(parentInstance interface{}, structType goo.Struct, request HttpRequest) {
	headerParams := request.GetHeader()
	if len(headerParams) > 0 && structType.GetFieldCount() > 0 {
		for _, field := range structType.GetFields() {
			tag, err := resolver.getBindingTag(field)
			if err != nil {
				continue
			}
			if value, ok := headerParams[tag.Value]; ok {
				resolver.bindField(parentInstance, field, value[0])
			}
		}
	}
}

func (resolver RequestMethodParameterResolver) getBindingTag(field goo.Field) (goo.Tag, error) {
	tag, err := field.GetTagByName("json")
	if err != nil {
		tag, err = field.GetTagByName("yaml")
	}
	return tag, err
}

func (resolver RequestMethodParameterResolver) bindField(parentInstance interface{}, field goo.Field, value interface{}) {
	if resolver.converterService.CanConvert(goo.GetType(value), field.GetType()) {
		result, err := resolver.converterService.Convert(value, goo.GetType(value), field.GetType())
		if err != nil {
			return
		}
		field.SetValue(parentInstance, result)
	}
}
