package di

import (
	"errors"
	"fmt"
	"github.com/cheivin/di/van"
	"reflect"
	"unsafe"
)

type (
	DI struct {
		beanDefinitionMap map[string]definition  // beanName:bean定义
		prototypeMap      map[string]interface{} // beanName:初始化的bean
		beanMap           map[string]interface{} // beanName:bean实例
		loaded            bool
		unsafe            bool
		valueStore        ValueStore
	}

	// BeanConstruct Bean实例创建时
	BeanConstruct interface {
		BeanConstruct()
	}

	// PreInitialize Bean实例依赖注入前
	PreInitialize interface {
		PreInitialize()
	}

	// AfterPropertiesSet Bean实例注入完成
	AfterPropertiesSet interface {
		AfterPropertiesSet()
	}

	Initialized interface {
		Initialized()
	}
)

var (
	ErrBean       = errors.New("error bean")
	ErrDefinition = errors.New("error definition")
	ErrLoaded     = errors.New("DI loaded")
)

func New() *DI {
	return &DI{
		beanDefinitionMap: map[string]definition{},
		prototypeMap:      map[string]interface{}{},
		beanMap:           map[string]interface{}{},
		valueStore:        van.New(),
	}
}

func (di *DI) UnsafeMode(open bool) *DI {
	di.unsafe = open
	return di
}

// RegisterBean 注册一个已生成的bean，根据bean类型生成beanName
func (di *DI) RegisterBean(bean interface{}) *DI {
	return di.RegisterNamedBean("", bean)
}

// RegisterNamedBean 以指定名称注册一个bean
func (di *DI) RegisterNamedBean(beanName string, bean interface{}) *DI {
	if di.loaded {
		panic(ErrLoaded)
	}
	if !IsPtr(bean) {
		panic(fmt.Errorf("%w: bean must be a pointer", ErrBean))
	}
	if beanName == "" {
		beanName = GetBeanName(bean)
	}
	if _, exist := di.beanMap[beanName]; exist {
		panic(fmt.Errorf("%w: bean %s already exists", ErrBean, beanName))
	}
	di.beanMap[beanName] = bean
	return di
}

func (di *DI) Provide(prototype interface{}) *DI {
	di.ProvideWithBeanName("", prototype)
	return di
}

func (di *DI) ProvideWithBeanName(beanName string, beanType interface{}) *DI {
	if di.loaded {
		panic(ErrLoaded)
	}
	if beanName == "" {
		beanName = GetBeanName(beanType)
	}
	var prototype reflect.Type
	if IsPtr(beanType) {
		prototype = reflect.TypeOf(beanType).Elem()
	} else {
		prototype = reflect.TypeOf(beanType)
	}
	// 检查beanDefinition重复
	if existDefinition, exist := di.beanDefinitionMap[beanName]; exist {
		panic(fmt.Errorf("%w: bean %s already defined by %s", ErrDefinition, beanName, existDefinition.Type.String()))
	} else {
		di.beanDefinitionMap[beanName] = newDefinition(beanName, prototype)
	}
	// 检查bean重复
	if _, exist := di.beanMap[beanName]; exist {
		panic(fmt.Errorf("%w: bean %s already exists", ErrBean, beanName))
	}
	return di
}

func (di *DI) GetBean(beanName string) (interface{}, bool) {
	bean, ok := di.beanMap[beanName]
	return bean, ok
}

func (di *DI) UseValueStore(v ValueStore) {
	di.valueStore = v
}

func (di *DI) Property() ValueStore {
	return di.valueStore
}

func (di *DI) Load() {
	if di.loaded {
		panic(ErrLoaded)
	}
	di.initializeBean()
	di.aware()
	di.initialized()
}

// initializeBean 初始化bean对象
func (di *DI) initializeBean() {
	// 创建类型的指针对象
	for beanName, def := range di.beanDefinitionMap {
		prototype := reflect.New(def.Type).Interface()
		di.prototypeMap[beanName] = prototype
	}
	// 遍历触发BeanConstruct方法
	for _, prototype := range di.prototypeMap {
		if construct, ok := prototype.(BeanConstruct); ok {
			construct.BeanConstruct()
		}
	}
}

// aware 注入依赖
func (di *DI) aware() {
	for beanName, prototype := range di.prototypeMap {
		// 注入前方法
		if initialize, ok := prototype.(PreInitialize); ok {
			initialize.PreInitialize()
		}
		def := di.beanDefinitionMap[beanName]
		bean := reflect.ValueOf(prototype).Elem()
		for filedName, awareInfo := range def.awareMap {
			var awareBean interface{}
			var ok bool
			if awareBean, ok = di.beanMap[awareInfo.beanName]; !ok {
				// 手动注册的bean中找不到，尝试查找原型定义
				if awareBean, ok = di.prototypeMap[awareInfo.beanName]; !ok {
					panic(fmt.Errorf("%w: %s notfound for %s(%s.%s)",
						ErrBean,
						awareInfo.beanName,
						beanName,
						def.Type.String(),
						filedName,
					))
				}
			}
			// 注入
			value := reflect.ValueOf(awareBean)
			// 类型检查
			if awareInfo.isPtr { // 指针类型
				if !value.Type().AssignableTo(awareInfo.beanType) {
					panic(fmt.Errorf("%w: %s(%s) not match for %s(%s.%s) need type %s",
						ErrBean,
						awareInfo.beanName, value.Type().String(),
						beanName,
						def.Type.String(),
						filedName,
						awareInfo.beanType.String(),
					))
				}
			} else { // 接口类型
				if !value.Type().Implements(awareInfo.beanType) {
					panic(fmt.Errorf("%w: %s(%s) not implements interface %s for %s(%s.%s)",
						ErrBean,
						awareInfo.beanName, value.Type().String(),
						awareInfo.beanType.String(),
						beanName,
						def.Type.String(),
						filedName,
					))
				}
			}

			// 设置值
			if di.unsafe {
				field := bean.FieldByName(filedName)
				field = reflect.NewAt(field.Type(), unsafe.Pointer(field.UnsafeAddr())).Elem()
				field.Set(value)
			} else {
				bean.FieldByName(filedName).Set(value)
			}
		}
		// 注入后方法
		if propertiesSet, ok := prototype.(AfterPropertiesSet); ok {
			propertiesSet.AfterPropertiesSet()
		}
		// 加载为bean
		di.beanMap[beanName] = prototype
	}
}

func (di *DI) initialized() {
	for _, prototype := range di.prototypeMap {
		// 初始化完成
		if propertiesSet, ok := prototype.(Initialized); ok {
			propertiesSet.Initialized()
		}
	}
}
