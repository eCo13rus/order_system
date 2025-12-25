// Package logger предоставляет структурированное логирование на базе zerolog.
// Поддерживает JSON формат для production и pretty-print для development.
// Все сообщения логов пишутся на русском языке.
package logger

import (
	"io"
	"os"
	"strings"
	"time"

	"github.com/rs/zerolog"
)

// log - глобальный экземпляр логгера.
// Инициализируется при вызове Init() или автоматически при первом использовании.
var log zerolog.Logger

// Config содержит настройки для инициализации логгера.
type Config struct {
	// Level задает минимальный уровень логирования.
	// Допустимые значения: "debug", "info", "warn", "error".
	// По умолчанию: "info".
	Level string

	// Pretty включает форматированный вывод для разработки.
	// При Pretty=true логи выводятся в читаемом формате с цветами.
	// При Pretty=false логи выводятся в JSON формате для production.
	Pretty bool

	// Output задает writer для вывода логов.
	// По умолчанию: os.Stdout.
	Output io.Writer
}

// init инициализирует логгер с настройками по умолчанию.
// Вызывается автоматически при импорте пакета.
func init() {
	// Проверяем переменную окружения LOG_PRETTY для автоматического
	// включения pretty-print в development среде.
	pretty := strings.ToLower(os.Getenv("LOG_PRETTY")) == "true"

	// Получаем уровень логирования из переменной окружения.
	level := os.Getenv("LOG_LEVEL")
	if level == "" {
		level = "info"
	}

	Init(Config{
		Level:  level,
		Pretty: pretty,
	})
}

// Init инициализирует глобальный логгер с заданной конфигурацией.
// Должен вызываться в начале работы приложения для настройки логирования.
func Init(cfg Config) {
	var output io.Writer = os.Stdout

	// Используем переданный output, если он задан.
	if cfg.Output != nil {
		output = cfg.Output
	}

	// Настраиваем pretty-print для development среды.
	// ConsoleWriter форматирует логи в читаемый вид с цветами.
	if cfg.Pretty {
		output = zerolog.ConsoleWriter{
			Out:        output,
			TimeFormat: time.RFC3339,
			// Настраиваем формат для лучшей читаемости.
			NoColor: false,
		}
	}

	// Парсим уровень логирования из строки.
	level := parseLevel(cfg.Level)

	// Создаем логгер с базовыми настройками:
	// - timestamp для каждой записи
	// - caller для указания места вызова (файл:строка)
	log = zerolog.New(output).
		Level(level).
		With().
		Timestamp().
		Caller().
		Logger()

	// Устанавливаем глобальный уровень для zerolog.
	zerolog.SetGlobalLevel(level)

	// Настраиваем формат времени.
	zerolog.TimeFieldFormat = time.RFC3339
}

// parseLevel преобразует строковое представление уровня в zerolog.Level.
// При неизвестном уровне возвращает InfoLevel.
func parseLevel(level string) zerolog.Level {
	switch strings.ToLower(level) {
	case "debug":
		return zerolog.DebugLevel
	case "info":
		return zerolog.InfoLevel
	case "warn", "warning":
		return zerolog.WarnLevel
	case "error":
		return zerolog.ErrorLevel
	case "fatal":
		return zerolog.FatalLevel
	case "panic":
		return zerolog.PanicLevel
	case "trace":
		return zerolog.TraceLevel
	default:
		return zerolog.InfoLevel
	}
}

// Debug создает событие лога уровня debug.
// Используется для детальной отладочной информации.
// Пример: logger.Debug().Str("user_id", "123").Msg("Начало обработки запроса")
func Debug() *zerolog.Event {
	return log.Debug()
}

// Info создает событие лога уровня info.
// Используется для информационных сообщений о нормальной работе.
// Пример: logger.Info().Str("order_id", "456").Msg("Заказ успешно создан")
func Info() *zerolog.Event {
	return log.Info()
}

// Warn создает событие лога уровня warn.
// Используется для предупреждений о потенциальных проблемах.
// Пример: logger.Warn().Int("retry", 3).Msg("Повторная попытка подключения")
func Warn() *zerolog.Event {
	return log.Warn()
}

// Error создает событие лога уровня error.
// Используется для ошибок, не приводящих к остановке приложения.
// Пример: logger.Error().Err(err).Msg("Ошибка при обработке платежа")
func Error() *zerolog.Event {
	return log.Error()
}

// Fatal создает событие лога уровня fatal и завершает приложение.
// Используется для критических ошибок, после которых продолжение невозможно.
// ВНИМАНИЕ: после вызова Msg() приложение завершится с кодом 1.
func Fatal() *zerolog.Event {
	return log.Fatal()
}

// Panic создает событие лога уровня panic и вызывает panic.
// Используется в исключительных случаях.
// ВНИМАНИЕ: после вызова Msg() будет вызван panic.
func Panic() *zerolog.Event {
	return log.Panic()
}

// With создает новый логгер с дополнительными полями.
// Возвращает zerolog.Context для добавления полей.
// Пример:
//
//	serviceLog := logger.With().Str("service", "user").Logger()
//	serviceLog.Info().Msg("Сервис запущен")
func With() zerolog.Context {
	return log.With()
}

// Logger возвращает глобальный экземпляр zerolog.Logger.
// Используется когда нужен прямой доступ к логгеру.
func Logger() zerolog.Logger {
	return log
}

// SetGlobalLogger устанавливает глобальный логгер.
// Полезно для тестирования или специальных случаев.
func SetGlobalLogger(l zerolog.Logger) {
	log = l
}
