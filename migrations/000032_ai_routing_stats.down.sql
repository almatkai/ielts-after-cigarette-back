-- Explicit rollback discards operational telemetry, not student exam history.
DROP TABLE ai_provider_rotation;
DROP TABLE ai_provider_calls;
DROP TABLE ai_provider_routing;
