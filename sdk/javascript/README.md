# InvoiceParser JavaScript SDK

Parse invoices with one function call. Works in Node.js and browsers.

## Install

```bash
npm install invoiceparser
```

## Node.js

```javascript
const { InvoiceParser } = require("invoiceparser");

const client = new InvoiceParser("inv_your_key_here");

// Parse a single invoice
const invoice = await client.parse("invoice.pdf");
console.log(`Total: ${invoice.currency} ${invoice.total}`);
console.log(`Vendor: ${invoice.vendor.name}`);
```

## Browser

```javascript
import { InvoiceParser } from "invoiceparser";

const client = new InvoiceParser("inv_your_key_here");

// Parse from file input
const invoice = await client.parseFile(fileInput.files[0]);
console.log(`Total: ${invoice.total}`);
```

## Batch Processing (Pro plan)

```javascript
const results = await client.parseBatch(["inv1.pdf", "inv2.png"]);
for (const r of results.results) {
  if (r.success) console.log(`${r.filename}: ${r.data.total}`);
}

// Async with webhook
const job = await client.parseBatch(["inv1.pdf"], {
  webhookUrl: "https://your-app.com/webhook",
});
```

## TypeScript

Full type definitions included. No `@types` package needed.

```typescript
import { InvoiceParser, InvoiceData } from "invoiceparser";

const client = new InvoiceParser("inv_...");
const data: InvoiceData = await client.parse("invoice.pdf");
```

## Environment Variable

```bash
export INVOICEPARSER_API_KEY=inv_your_key_here
```

```javascript
const client = new InvoiceParser(); // reads from env
```
