# Stripe Billing Setup

## 1. Create Stripe Account
Go to https://dashboard.stripe.com and create an account.

## 2. Create Products & Prices

In Stripe Dashboard → Products, create two products:

**Starter Plan - $29/mo**
- Name: InvoiceParser Starter
- Price: $29.00 / month (recurring)
- Copy the Price ID (starts with `price_`)

**Pro Plan - $99/mo**
- Name: InvoiceParser Pro
- Price: $99.00 / month (recurring)
- Copy the Price ID (starts with `price_`)

## 3. Set Up Webhook

In Stripe Dashboard → Developers → Webhooks:
- Endpoint URL: `https://invoice-parser-api-gnmr.onrender.com/v1/webhooks/stripe`
- Events to listen for:
  - `checkout.session.completed`
  - `customer.subscription.deleted`
- Copy the Webhook Signing Secret (starts with `whsec_`)

## 4. Set Fly.io Secrets

```bash
flyctl secrets set \
  STRIPE_SECRET_KEY=sk_live_... \
  STRIPE_WEBHOOK_SECRET=whsec_... \
  STRIPE_STARTER_PRICE_ID=price_... \
  STRIPE_PRO_PRICE_ID=price_...
```

## 5. Test the Checkout Flow

```bash
curl -X POST https://invoice-parser-api-gnmr.onrender.com/v1/billing/checkout \
  -H "X-API-Key: inv_your_key" \
  -H "Content-Type: application/json" \
  -d '{
    "plan": "starter",
    "success_url": "https://invoice-parser-api-gnmr.onrender.com/?upgraded=true",
    "cancel_url": "https://invoice-parser-api-gnmr.onrender.com/?cancelled=true"
  }'
```

This returns a `checkout_url` — redirect the user there to complete payment.

## 6. Verify Webhook

After a test payment, check Fly.io logs:
```bash
flyctl logs
```

You should see: `"msg":"plan upgraded","email":"...","plan":"starter"`
