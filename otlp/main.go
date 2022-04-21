package main

import (
	"context"
	"fmt"
	"log"
	"math"
	"math/rand"
	"os"
	"os/signal"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/metric/global"
	"go.opentelemetry.io/otel/metric/instrument"
	"go.opentelemetry.io/otel/propagation"
	controller "go.opentelemetry.io/otel/sdk/metric/controller/basic"
	processor "go.opentelemetry.io/otel/sdk/metric/processor/basic"
	"go.opentelemetry.io/otel/sdk/metric/selector/simple"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.7.0"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// Initializes an OTLP exporter, and configures the corresponding trace and
// metric providers.
func initProvider() {
	ctx := context.Background()

	res, err := resource.New(ctx,
		resource.WithAttributes(
			// the service name used to display traces in backends
			semconv.ServiceNameKey.String("test-service"),
		),
	)
	if err != nil {
		log.Printf("failed to create resource: %s", err)
		return
	}

	// If the OpenTelemetry Collector is running on a local cluster (minikube or
	// microk8s), it should be accessible through the NodePort service at the
	// `localhost:30080` endpoint. Otherwise, replace `localhost` with the
	// endpoint of your cluster. If you run the app inside k8s, then you can
	// probably connect directly to the service through dns
	conn, err := grpc.DialContext(
		ctx,
		"datadog-agent:30080",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(),
	)
	if err != nil {
		log.Printf("failed to create gRPC connection: %s", err)
		return
	}

	metricClient := otlpmetricgrpc.NewClient(otlpmetricgrpc.WithGRPCConn(conn))
	metricExp, err := otlpmetric.New(ctx, metricClient)
	if err != nil {
		log.Printf("failed to create gRPC connection: %s", err)
		return
	}
	pusher := controller.New(
		processor.NewFactory(
			simple.NewWithHistogramDistribution(),
			metricExp,
		),
		controller.WithExporter(metricExp),
	)
	global.SetMeterProvider(pusher)
	if err := pusher.Start(ctx); err != nil {
		log.Printf("failed to start the pusher: %s", err)
		return
	}

	// Set up a trace exporter
	traceExporter, err := otlptracegrpc.New(ctx, otlptracegrpc.WithGRPCConn(conn))
	if err != nil {
		log.Printf("failed to create trace exporter: %s", err)
		return
	}
	// Register the trace exporter with a TracerProvider, using a batch
	// span processor to aggregate spans before export.
	bsp := sdktrace.NewBatchSpanProcessor(traceExporter)
	tracerProvider := sdktrace.NewTracerProvider(
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
		sdktrace.WithResource(res),
		sdktrace.WithSpanProcessor(bsp),
	)
	otel.SetTracerProvider(tracerProvider)

	// set global propagator to tracecontext (the default is no-op).
	otel.SetTextMapPropagator(propagation.TraceContext{})
}

func fib(n uint) uint64 {
	if n <= 1 {
		return uint64(n)
	}
	var n2, n1 uint64 = 0, 1
	for i := uint(2); i <= n; i++ {
		n2, n1 = n1, n1+n2
	}
	return n2 + n1
}

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	rand.Seed(time.Now().UnixNano())
	log.SetOutput(os.Stdout)

	meter := global.Meter("test-meter")
	fibDuration, _ := meter.SyncInt64().UpDownCounter(
		"otlp_client/fib_duration",
		instrument.WithDescription("The duration for calculating a fibonacci number in milliseconds"),
		instrument.WithUnit("ms"),
	)

	log.Printf("Initialize a provider ...")
	initProvider()

	// labels represent additional key-value descriptors that can be bound to a
	// metric observer or recorder.
	commonLabels := []attribute.KeyValue{
		attribute.String("labelA", "chocolate"),
		attribute.String("labelB", "raspberry"),
		attribute.String("labelC", "vanilla"),
	}

	// work begins
	tracer := otel.Tracer("test-tracer")
	ctx, span := tracer.Start(
		ctx,
		"CollectorExporter-Example",
		trace.WithAttributes(commonLabels...),
	)
	defer span.End()
	for {
		select {
		case <-ctx.Done():
			log.Printf("Done!")
		default:
		}
		now := time.Now()
		ui := uint(rand.Intn(200))
		_, s := tracer.Start(ctx, fmt.Sprintf("fib-%d", ui))
		f := fib(ui)
		fibDuration.Add(ctx, time.Since(now).Milliseconds(), commonLabels...)
		<-time.After(time.Duration(math.Sqrt(float64(f))))
		log.Printf("fib(%d) = %d", ui, f)
		s.End()
	}
}
