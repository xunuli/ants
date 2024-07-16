package ants

import "time"

// Option represents the optional function.
type Option func(opts *Options)

func loadOptions(options ...Option) *Options {
	opts := new(Options)
	for _, option := range options {
		option(opts)
	}
	return opts
}

// Options contains all options which will be applied when instantiating an ants pool.
// options 包含了在实例化ants协程池时将应用的配置的所有选项
type Options struct {
	// ExpiryDuration is a period for the scavenger goroutine to clean up those expired workers,
	// the scavenger scans all workers every `ExpiryDuration` and clean up those workers that haven't been
	// used for more than `ExpiryDuration`.
	// ExpiryDuration 是清除过期worker的周期
	// 清除协程会每个 ExpiryDuration 时间扫描所有工作者，并清除那些空闲时间超过ExpierDuration的worker
	ExpiryDuration time.Duration

	// PreAlloc indicates whether to make memory pre-allocation when initializing Pool.
	// PreAlloc 标识在初始化过程中是否进行内存预先分配
	PreAlloc bool

	// Max number of goroutine blocking on pool.Submit.
	// 阻塞模式下，最多阻塞等待submit的协程池数量
	// 0 (default value) means no such limit.
	// 0 意味着没有限制
	MaxBlockingTasks int

	// When Nonblocking is true, Pool.Submit will never be blocked.
	// Nonblocking为true时，pool.Sibmit将不阻塞
	// ErrPoolOverload will be returned when Pool.Submit cannot be done at once.
	// 不阻塞时，当submit没法完成时，将返回ErrPoolOverload错误
	// When Nonblocking is true, MaxBlockingTasks is inoperative.
	// Nonblocking 为true时， MaxBlockingTasks无效
	Nonblocking bool

	// PanicHandler is used to handle panics from each worker goroutine.
	// PanicHandler用于处理每一个worker的panic
	// if nil, panics will be thrown out again from worker goroutines.
	// 如果为nil，则panic将从worker中重新抛出
	PanicHandler func(interface{})

	// Logger is the customized logger for logging info, if it is not set,
	// default standard logger from log package is used.
	// Logger 自定义日志记录器，如果不指定将使用默认的日志
	Logger Logger

	// When DisablePurge is true, workers are not purged and are resident.
	// 当DisablePurge为true时，worker不会被清除
	DisablePurge bool
}

// WithOptions accepts the whole options config.
func WithOptions(options Options) Option {
	return func(opts *Options) {
		*opts = options
	}
}

// WithExpiryDuration sets up the interval time of cleaning up goroutines.
func WithExpiryDuration(expiryDuration time.Duration) Option {
	return func(opts *Options) {
		opts.ExpiryDuration = expiryDuration
	}
}

// WithPreAlloc indicates whether it should malloc for workers.
func WithPreAlloc(preAlloc bool) Option {
	return func(opts *Options) {
		opts.PreAlloc = preAlloc
	}
}

// WithMaxBlockingTasks sets up the maximum number of goroutines that are blocked when it reaches the capacity of pool.
func WithMaxBlockingTasks(maxBlockingTasks int) Option {
	return func(opts *Options) {
		opts.MaxBlockingTasks = maxBlockingTasks
	}
}

// WithNonblocking indicates that pool will return nil when there is no available workers.
func WithNonblocking(nonblocking bool) Option {
	return func(opts *Options) {
		opts.Nonblocking = nonblocking
	}
}

// WithPanicHandler sets up panic handler.
func WithPanicHandler(panicHandler func(interface{})) Option {
	return func(opts *Options) {
		opts.PanicHandler = panicHandler
	}
}

// WithLogger sets up a customized logger.
func WithLogger(logger Logger) Option {
	return func(opts *Options) {
		opts.Logger = logger
	}
}

// WithDisablePurge indicates whether we turn off automatically purge.
func WithDisablePurge(disable bool) Option {
	return func(opts *Options) {
		opts.DisablePurge = disable
	}
}
