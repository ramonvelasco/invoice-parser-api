# InvoiceParser Python SDK

Parse invoices with one function call. Send a PDF or image, get clean structured JSON.

## Install

```bash
pip install invoiceparser
```

## Quick Start

```python
from invoiceparser import InvoiceParser

client = InvoiceParser(api_key="inv_your_key_here")

# Parse a single invoice
invoice = client.parse("invoice.pdf")
print(f"Total: {invoice['currency']} {invoice['total']}")
print(f"Vendor: {invoice['vendor']['name']}")

# Check usage
usage = client.usage()
print(f"Used: {usage['used_calls']}/{usage['max_calls']}")
```

## Batch Processing (Pro plan)

```python
results = client.parse_batch(["inv1.pdf", "inv2.png", "inv3.jpg"])
for r in results["results"]:
    if r["success"]:
        print(f"{r['filename']}: {r['data']['total']}")

# Async with webhook
job = client.parse_batch(
    ["inv1.pdf", "inv2.pdf"],
    webhook_url="https://your-app.com/webhook"
)
print(f"Job {job['job_id']} queued")
```

## Environment Variable

```bash
export INVOICEPARSER_API_KEY=inv_your_key_here
```

```python
client = InvoiceParser()  # reads from env
```

## API Reference

| Method | Description |
|--------|-------------|
| `parse(file_path)` | Parse a single invoice |
| `parse_batch(file_paths, webhook_url=None)` | Batch parse (Pro) |
| `get_batch_job(job_id)` | Check batch job status |
| `usage()` | Get usage stats |
| `dashboard()` | Full dashboard with daily breakdown |
| `create_checkout(plan, success_url, cancel_url)` | Upgrade plan |
