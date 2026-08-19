from __future__ import annotations

from typing import Any

from sqlalchemy import text
from sqlalchemy.orm import Session

QUEUED_RERUN_JOBS: list[dict[str, Any]] = []


def enqueue_rerun_job(forecast_id: int, params: dict[str, Any]) -> dict[str, Any]:
    """Minimal placeholder for queueing work."""
    job = {
        "job_id": f"rerun-{len(QUEUED_RERUN_JOBS) + 1}",
        "forecast_id": forecast_id,
        "params": params,
    }
    QUEUED_RERUN_JOBS.append(job)
    return job


class ForecastService:
    def __init__(self, db: Session) -> None:
        self.db = db

    def rerun_forecast(
        self,
        *,
        forecast_id: int,
        requested_by: str,
    ) -> dict[str, Any]:
        forecast = self.db.execute(
            text(
                """
                SELECT id, company_id, model_version, input_params
                FROM forecasts
                WHERE id = :forecast_id
                """
            ),
            {"forecast_id": forecast_id},
        ).mappings().first()

        if forecast is None:
            raise LookupError(f"Forecast {forecast_id} not found")

        job = enqueue_rerun_job(
            forecast_id=forecast["id"],
            params={
                "forecast_id": forecast["id"],
                "model_version": forecast["model_version"],
                "input_params": forecast["input_params"],
                "requested_by": requested_by,
                "reason": "manual-rerun",
            },
        )

        return {
            "forecast_id": forecast["id"],
            "status": "queued",
            "job_id": job["job_id"],
        }
