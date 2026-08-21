export class FarfieldError extends Error {
  override readonly name: string = "FarfieldError";
}

export class APIError extends FarfieldError {
  override readonly name = "APIError";

  constructor(
    readonly statusCode: number,
    readonly code: string,
    message: string,
    readonly retryable: boolean,
  ) {
    super(code ? `${code}: ${message}` : `HTTP ${statusCode}: ${message}`);
  }
}

export class TransportError extends FarfieldError {
  override readonly name = "TransportError";

  constructor(
    message: string,
    readonly cause?: unknown,
  ) {
    super(message);
  }
}

export class DroppedEvent extends FarfieldError {
  override readonly name = "DroppedEvent";

  constructor(message = "farfield: event dropped by beforeSend") {
    super(message);
  }
}
