"""InvoiceParser API client."""

import os
from pathlib import Path
from typing import Any, Optional

import requests


class InvoiceParserError(Exception):
    """Raised when the API returns an error."""

    def __init__(self, status_code: int, error: str, message: str = ""):
        self.status_code = status_code
        self.error = error
        self.message = message
        super().__init__(f"{error}: {message}" if message else error)


class InvoiceParser:
    """Client for the InvoiceParser API.

    Usage:
        client = InvoiceParser(api_key="inv_...")
        result = client.parse("invoice.pdf")
        print(result["total"])
    """

    DEFAULT_BASE_URL = "https://invoiceparser-api.fly.dev"

    def __init__(
        self,
        api_key: Optional[str] = None,
        base_url: Optional[str] = None,
    ):
        self.api_key = api_key or os.environ.get("INVOICEPARSER_API_KEY", "")
        if not self.api_key:
            raise ValueError(
                "API key required. Pass api_key= or set INVOICEPARSER_API_KEY env var."
            )
        self.base_url = (base_url or self.DEFAULT_BASE_URL).rstrip("/")
        self._session = requests.Session()
        self._session.headers["X-API-Key"] = self.api_key

    def parse(self, file_path: str) -> dict[str, Any]:
        """Parse a single invoice file.

        Args:
            file_path: Path to PDF, PNG, JPG, WebP, or GIF file.

        Returns:
            Parsed invoice data dict.
        """
        path = Path(file_path)
        with open(path, "rb") as f:
            resp = self._session.post(
                f"{self.base_url}/v1/parse/invoice",
                files={"file": (path.name, f)},
            )
        data = resp.json()
        if not resp.ok:
            raise InvoiceParserError(
                resp.status_code,
                data.get("error", "unknown_error"),
                data.get("message", ""),
            )
        return data["data"]

    def parse_batch(
        self,
        file_paths: list[str],
        webhook_url: Optional[str] = None,
    ) -> dict[str, Any]:
        """Parse multiple invoices (Pro plan required).

        Args:
            file_paths: List of file paths (max 20).
            webhook_url: Optional URL for async webhook delivery.

        Returns:
            Batch results dict (or job info if webhook_url provided).
        """
        files = []
        opened = []
        try:
            for fp in file_paths:
                path = Path(fp)
                f = open(path, "rb")
                opened.append(f)
                files.append(("files", (path.name, f)))

            data = {}
            if webhook_url:
                data["webhook_url"] = webhook_url

            resp = self._session.post(
                f"{self.base_url}/v1/parse/batch",
                files=files,
                data=data,
            )
        finally:
            for f in opened:
                f.close()

        result = resp.json()
        if not resp.ok:
            raise InvoiceParserError(
                resp.status_code,
                result.get("error", "unknown_error"),
                result.get("message", ""),
            )
        return result

    def get_batch_job(self, job_id: str) -> dict[str, Any]:
        """Get batch job status.

        Args:
            job_id: The batch job ID returned from parse_batch.

        Returns:
            Job status dict with results if completed.
        """
        resp = self._session.get(f"{self.base_url}/v1/parse/batch/{job_id}")
        data = resp.json()
        if not resp.ok:
            raise InvoiceParserError(
                resp.status_code,
                data.get("error", "unknown_error"),
                data.get("message", ""),
            )
        return data

    def usage(self) -> dict[str, Any]:
        """Get current usage stats.

        Returns:
            Dict with plan, used_calls, max_calls, today_calls, month_calls.
        """
        resp = self._session.get(f"{self.base_url}/v1/usage")
        data = resp.json()
        if not resp.ok:
            raise InvoiceParserError(
                resp.status_code,
                data.get("error", "unknown_error"),
                data.get("message", ""),
            )
        return data

    def dashboard(self) -> dict[str, Any]:
        """Get full dashboard data including daily breakdown.

        Returns:
            Dict with usage stats, daily_usage array, and recent_logs.
        """
        resp = self._session.get(f"{self.base_url}/v1/dashboard")
        data = resp.json()
        if not resp.ok:
            raise InvoiceParserError(
                resp.status_code,
                data.get("error", "unknown_error"),
                data.get("message", ""),
            )
        return data

    def create_checkout(
        self,
        plan: str,
        success_url: str,
        cancel_url: str,
    ) -> str:
        """Create a Stripe checkout session for plan upgrade.

        Args:
            plan: "starter" or "pro"
            success_url: Redirect URL after successful payment.
            cancel_url: Redirect URL if user cancels.

        Returns:
            Stripe checkout URL.
        """
        resp = self._session.post(
            f"{self.base_url}/v1/billing/checkout",
            json={
                "plan": plan,
                "success_url": success_url,
                "cancel_url": cancel_url,
            },
        )
        data = resp.json()
        if not resp.ok:
            raise InvoiceParserError(
                resp.status_code,
                data.get("error", "unknown_error"),
                data.get("message", ""),
            )
        return data["checkout_url"]
