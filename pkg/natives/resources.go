package natives

import (
	"github.com/dop251/goja"
	"github.com/go-go-golems/rag-workbench/pkg/rtenv"
	"github.com/pkg/errors"
	"io"
)

func ownResource(vm *goja.Runtime, env rtenv.Env, object *goja.Object, resource io.Closer) *goja.Object {
	if env.Resources == nil {
		_ = resource.Close()
		panic(vm.NewGoError(errors.New("native resource registry is required")))
	}
	handle, err := env.Resources.Adopt(resource)
	if err != nil {
		closeErr := resource.Close()
		panic(vm.NewGoError(errors.Errorf("adopt native resource: %v (close: %v)", err, closeErr)))
	}
	if err := object.Set("close", func() {
		if err := handle.Close(); err != nil {
			panic(vm.NewGoError(err))
		}
	}); err != nil {
		_ = handle.Close()
		panic(vm.NewGoError(err))
	}
	return object
}
