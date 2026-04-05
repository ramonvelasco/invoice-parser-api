/**
 * InvoiceParser JavaScript SDK
 *
 * Usage:
 *   // Node.js
 *   const { InvoiceParser } = require("invoiceparser");
 *   const client = new InvoiceParser("inv_...");
 *   const invoice = await client.parse("invoice.pdf");
 *
 *   // Browser
 *   const client = new InvoiceParser("inv_...");
 *   const invoice = await client.parseFile(fileInput.files[0]);
 */

const fs = require("fs");
const path = require("path");

class InvoiceParserError extends Error {
  constructor(statusCode, error, message) {
    super(message || error);
    this.name = "InvoiceParserError";
    this.statusCode = statusCode;
    this.error = error;
  }
}

class InvoiceParser {
  static DEFAULT_BASE_URL = "https://invoiceparser-api.fly.dev";

  /**
   * @param {string} apiKey - Your InvoiceParser API key (inv_...)
   * @param {object} [options]
   * @param {string} [options.baseUrl] - Override the API base URL
   */
  constructor(apiKey, options = {}) {
    this.apiKey = apiKey || process.env.INVOICEPARSER_API_KEY;
    if (!this.apiKey) {
      throw new Error(
        "API key required. Pass apiKey or set INVOICEPARSER_API_KEY env var."
      );
    }
    this.baseUrl = (options.baseUrl || InvoiceParser.DEFAULT_BASE_URL).replace(
      /\/$/,
      ""
    );
  }

  /**
   * Parse a single invoice from a file path (Node.js only).
   * @param {string} filePath - Path to PDF, PNG, JPG, WebP, or GIF
   * @returns {Promise<object>} Parsed invoice data
   */
  async parse(filePath) {
    const FormData = (await import("form-data")).default;
    const form = new FormData();
    form.append("file", fs.createReadStream(filePath), path.basename(filePath));

    const resp = await fetch(`${this.baseUrl}/v1/parse/invoice`, {
      method: "POST",
      headers: { "X-API-Key": this.apiKey, ...form.getHeaders() },
      body: form,
    });

    const data = await resp.json();
    if (!resp.ok) {
      throw new InvoiceParserError(
        resp.status,
        data.error,
        data.message
      );
    }
    return data.data;
  }

  /**
   * Parse a single invoice from a File object (browser-compatible).
   * @param {File|Blob} file - File or Blob object
   * @returns {Promise<object>} Parsed invoice data
   */
  async parseFile(file) {
    const form = new FormData();
    form.append("file", file);

    const resp = await fetch(`${this.baseUrl}/v1/parse/invoice`, {
      method: "POST",
      headers: { "X-API-Key": this.apiKey },
      body: form,
    });

    const data = await resp.json();
    if (!resp.ok) {
      throw new InvoiceParserError(
        resp.status,
        data.error,
        data.message
      );
    }
    return data.data;
  }

  /**
   * Parse multiple invoices (Pro plan required, Node.js only).
   * @param {string[]} filePaths - Array of file paths (max 20)
   * @param {object} [options]
   * @param {string} [options.webhookUrl] - URL for async webhook delivery
   * @returns {Promise<object>} Batch results or job info
   */
  async parseBatch(filePaths, options = {}) {
    const FormData = (await import("form-data")).default;
    const form = new FormData();

    for (const fp of filePaths) {
      form.append("files", fs.createReadStream(fp), path.basename(fp));
    }
    if (options.webhookUrl) {
      form.append("webhook_url", options.webhookUrl);
    }

    const resp = await fetch(`${this.baseUrl}/v1/parse/batch`, {
      method: "POST",
      headers: { "X-API-Key": this.apiKey, ...form.getHeaders() },
      body: form,
    });

    const data = await resp.json();
    if (!resp.ok) {
      throw new InvoiceParserError(
        resp.status,
        data.error,
        data.message
      );
    }
    return data;
  }

  /**
   * Get batch job status.
   * @param {string} jobId - Batch job ID
   * @returns {Promise<object>} Job status with results if completed
   */
  async getBatchJob(jobId) {
    const resp = await fetch(`${this.baseUrl}/v1/parse/batch/${jobId}`, {
      headers: { "X-API-Key": this.apiKey },
    });
    const data = await resp.json();
    if (!resp.ok) {
      throw new InvoiceParserError(
        resp.status,
        data.error,
        data.message
      );
    }
    return data;
  }

  /**
   * Get current API usage statistics.
   * @returns {Promise<object>}
   */
  async usage() {
    const resp = await fetch(`${this.baseUrl}/v1/usage`, {
      headers: { "X-API-Key": this.apiKey },
    });
    const data = await resp.json();
    if (!resp.ok) {
      throw new InvoiceParserError(
        resp.status,
        data.error,
        data.message
      );
    }
    return data;
  }

  /**
   * Get full dashboard data.
   * @returns {Promise<object>}
   */
  async dashboard() {
    const resp = await fetch(`${this.baseUrl}/v1/dashboard`, {
      headers: { "X-API-Key": this.apiKey },
    });
    const data = await resp.json();
    if (!resp.ok) {
      throw new InvoiceParserError(
        resp.status,
        data.error,
        data.message
      );
    }
    return data;
  }

  /**
   * Create a Stripe checkout session for plan upgrade.
   * @param {string} plan - "starter" or "pro"
   * @param {string} successUrl - Redirect after payment
   * @param {string} cancelUrl - Redirect on cancel
   * @returns {Promise<string>} Stripe checkout URL
   */
  async createCheckout(plan, successUrl, cancelUrl) {
    const resp = await fetch(`${this.baseUrl}/v1/billing/checkout`, {
      method: "POST",
      headers: {
        "X-API-Key": this.apiKey,
        "Content-Type": "application/json",
      },
      body: JSON.stringify({
        plan,
        success_url: successUrl,
        cancel_url: cancelUrl,
      }),
    });

    const data = await resp.json();
    if (!resp.ok) {
      throw new InvoiceParserError(
        resp.status,
        data.error,
        data.message
      );
    }
    return data.checkout_url;
  }
}

module.exports = { InvoiceParser, InvoiceParserError };
