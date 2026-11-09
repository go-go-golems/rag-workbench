class RagError extends Error {
  declare code: string;
  declare detail?: unknown;
  constructor(code: string, message: string, detail?: unknown) {
    super(message);
    this.name = 'Rag' + code[0].toUpperCase() + code.slice(1) + 'Error';
    this.code = code;
    if (detail) this.detail = detail;
  }
}

export { RagError };
