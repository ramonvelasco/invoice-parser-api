export interface InvoiceData {
  invoice_number: string;
  invoice_date: string;
  due_date: string;
  currency: string;
  vendor: {
    name: string;
    address: string;
    email: string;
    phone: string;
    tax_id: string;
  };
  customer: {
    name: string;
    address: string;
    email: string;
    phone: string;
  };
  line_items: {
    description: string;
    quantity: number;
    unit_price: number;
    amount: number;
  }[];
  subtotal: number;
  tax_rate: number;
  tax_amount: number;
  discount: number;
  total: number;
  payment_terms: string;
  notes: string;
  confidence: number;
}

export interface BatchResult {
  index: number;
  filename: string;
  success: boolean;
  data?: InvoiceData;
  error?: string;
}

export interface BatchResponse {
  success: boolean;
  total: number;
  succeeded: number;
  failed: number;
  results: BatchResult[];
  latency_ms: number;
}

export interface BatchJobResponse {
  job_id: string;
  status: "pending" | "processing" | "completed" | "failed";
  file_count: number;
  completed: number;
  results?: BatchResult[];
}

export interface UsageResponse {
  plan: string;
  used_calls: number;
  max_calls: number;
  today_calls: number;
  month_calls: number;
}

export interface DashboardResponse extends UsageResponse {
  daily_usage: { date: string; calls: number }[];
  recent_logs: {
    endpoint: string;
    status: number;
    latency_ms: number;
    created_at: string;
  }[];
  member_since: string;
}

export class InvoiceParserError extends Error {
  statusCode: number;
  error: string;
  constructor(statusCode: number, error: string, message?: string);
}

export class InvoiceParser {
  constructor(
    apiKey?: string,
    options?: { baseUrl?: string }
  );

  parse(filePath: string): Promise<InvoiceData>;
  parseFile(file: File | Blob): Promise<InvoiceData>;
  parseBatch(
    filePaths: string[],
    options?: { webhookUrl?: string }
  ): Promise<BatchResponse | BatchJobResponse>;
  getBatchJob(jobId: string): Promise<BatchJobResponse>;
  usage(): Promise<UsageResponse>;
  dashboard(): Promise<DashboardResponse>;
  createCheckout(
    plan: "starter" | "pro",
    successUrl: string,
    cancelUrl: string
  ): Promise<string>;
}
