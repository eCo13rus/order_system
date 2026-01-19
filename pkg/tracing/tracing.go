// Package tracing предоставляет distributed tracing через OpenTelemetry + Jaeger.
//
// Основные концепции:
//   - Span: единица работы (HTTP запрос, gRPC вызов, DB query)
//   - Trace: цепочка связанных spans через все сервисы
//   - Context Propagation: передача trace_id между сервисами
//
// Как работает:
//  1. Gateway создаёт root span при входящем HTTP запросе
//  2. При gRPC вызове span передаётся через metadata (headers)
//  3. Каждый сервис создаёт child span и передаёт дальше
//  4. Все spans отправляются в Jaeger через OTLP протокол
//
// Использование:
//
//	shutdown, err := tracing.InitTracer("order-service", "localhost:4317")
//	if err != nil { ... }
//	defer shutdown(context.Background())
package tracing

import (
	"context"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.37.0"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"example.com/order-system/pkg/logger"
)

// Config содержит настройки tracing.
type Config struct {
	ServiceName    string // Имя сервиса (отображается в Jaeger UI)
	JaegerEndpoint string // OTLP endpoint Jaeger (например "localhost:4317")
	Enabled        bool   // Включить tracing (false для тестов)
}

// ShutdownFunc — функция для graceful shutdown трейсера.
type ShutdownFunc func(ctx context.Context) error

// InitTracer инициализирует OpenTelemetry с Jaeger exporter.
// Возвращает shutdown функцию для graceful завершения.
//
// Пример:
//
//	shutdown, err := tracing.InitTracer(tracing.Config{
//	    ServiceName:    "order-service",
//	    JaegerEndpoint: "localhost:4317",
//	    Enabled:        true,
//	})
//	defer shutdown(context.Background())
func InitTracer(cfg Config) (ShutdownFunc, error) {
	log := logger.With().Str("service", cfg.ServiceName).Logger()

	// Если tracing отключен — возвращаем no-op shutdown
	if !cfg.Enabled || cfg.JaegerEndpoint == "" {
		log.Info().Msg("Tracing отключен")
		return func(ctx context.Context) error { return nil }, nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Создаём gRPC соединение к Jaeger OTLP endpoint
	conn, err := grpc.NewClient(
		cfg.JaegerEndpoint,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		return nil, err
	}

	// Создаём OTLP exporter — отправляет spans в Jaeger
	exporter, err := otlptracegrpc.New(ctx, otlptracegrpc.WithGRPCConn(conn))
	if err != nil {
		return nil, err
	}

	// Resource описывает сервис (имя, версия, окружение)
	// Эти атрибуты видны в Jaeger UI для каждого span
	res, err := resource.Merge(
		resource.Default(),
		resource.NewWithAttributes(
			semconv.SchemaURL,
			semconv.ServiceName(cfg.ServiceName),     // Имя сервиса в Jaeger
			semconv.ServiceVersion("1.0.0"),          // Версия
			semconv.DeploymentEnvironmentName("dev"), // Окружение
		),
	)
	if err != nil {
		return nil, err
	}

	// TracerProvider управляет созданием spans
	// BatchSpanProcessor отправляет spans пачками (эффективнее)
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
		// Sampler определяет какие spans записывать
		// AlwaysSample — записываем всё (для dev), в prod можно ParentBased(TraceIDRatioBased(0.1))
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
	)

	// Устанавливаем глобальный TracerProvider
	otel.SetTracerProvider(tp)

	// Propagator определяет как trace_id передаётся между сервисами
	// W3C TraceContext — стандартный формат (header: traceparent)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{}, // W3C Trace Context
		propagation.Baggage{},      // Дополнительные данные
	))

	log.Info().
		Str("endpoint", cfg.JaegerEndpoint).
		Msg("Tracing инициализирован (Jaeger OTLP)")

	// Возвращаем shutdown функцию (закрываем и TracerProvider, и gRPC соединение)
	return func(ctx context.Context) error {
		log.Info().Msg("Завершение Tracing...")

		// Сначала завершаем TracerProvider (flush spans)
		if err := tp.Shutdown(ctx); err != nil {
			log.Error().Err(err).Msg("Ошибка завершения TracerProvider")
		}

		// Закрываем gRPC соединение к Jaeger
		if err := conn.Close(); err != nil {
			log.Error().Err(err).Msg("Ошибка закрытия gRPC соединения к Jaeger")
			return err
		}

		return nil
	}, nil
}
