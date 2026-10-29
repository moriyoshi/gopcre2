package probe

import (
	"runtime"
	"testing"

	n "github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/execution"
	"github.com/moriyoshi/nyanjit/compile"
)

type state struct{ Value uintptr }

var result uintptr

func build(b *testing.B, pointer bool) *execution.Code {
	b.Helper()
	builder := n.NewBuilder()
	var value n.Value
	if pointer {
		value = n.PointerParam[state](builder).Field("Value")
	} else {
		value = builder.ParamNative()
	}
	builder.Return(builder.Add(value, builder.ConstNative(1)))
	p, err := builder.Finish()
	if err != nil {
		b.Fatal(err)
	}
	code, err := n.Compile(p, compile.TargetSpec{}, compile.Func{
		ABI: abi.Go, Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 640},
	})
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { code.Close() })
	return code
}

//go:noinline
func addOne(x uintptr) uintptr { return x + 1 }

func BenchmarkBoundary(b *testing.B) {
	b.Run("GoIndirect", func(b *testing.B) {
		f := addOne
		b.ReportAllocs()
		for b.Loop() {
			result = f(41)
		}
	})
	b.Run("ScalarFunc", func(b *testing.B) {
		code := build(b, false)
		var f func(uintptr) uintptr
		if err := code.Func(&f); err != nil {
			b.Fatal(err)
		}
		if f(41) != 42 {
			b.Fatal("wrong result")
		}
		b.ReportAllocs()
		for b.Loop() {
			result = f(41)
		}
	})
	b.Run("ScalarCodeCall", func(b *testing.B) {
		code := build(b, false)
		b.ReportAllocs()
		for b.Loop() {
			var err error
			result, err = code.Call(41)
			if err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("PointerFunc", func(b *testing.B) {
		code := build(b, true)
		var f func(*state) uintptr
		if err := code.Func(&f); err != nil {
			b.Fatal(err)
		}
		s := &state{41}
		if f(s) != 42 {
			b.Fatal("wrong result")
		}
		b.ReportAllocs()
		for b.Loop() {
			result = f(s)
		}
		runtime.KeepAlive(s)
	})
	b.Run("PointerCodeCallWords", func(b *testing.B) {
		code := build(b, true)
		s := &state{41}
		b.ReportAllocs()
		for b.Loop() {
			var err error
			result, err = code.Call(execution.Words(s)...)
			if err != nil {
				b.Fatal(err)
			}
		}
	})
}
