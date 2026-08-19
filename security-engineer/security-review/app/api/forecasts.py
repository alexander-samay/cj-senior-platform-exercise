from __future__ import annotations

from typing import Any, Callable, Generator

from fastapi import APIRouter, Depends, HTTPException, status
from sqlalchemy.orm import Session

from app.services.forecast_service import ForecastService

router = APIRouter(prefix="/api/v1/forecasts", tags=["forecasts"])


def get_db() -> Generator[Session, None, None]:
    """Provided by application wiring in the real service."""
    raise NotImplementedError("Database session dependency is provided by the app.")


def get_current_user() -> dict[str, Any]:
    """Minimal auth fixture for this take-home."""
    return {
        "user_id": "user-123",
        "company_id": "company-alpha",
        "scopes": ["report:read", "forecast:write"],
    }


def require_scope(scope: str) -> Callable[[dict[str, Any]], dict[str, Any]]:
    def dependency(
        current_user: dict[str, Any] = Depends(get_current_user),
    ) -> dict[str, Any]:
        if scope not in current_user.get("scopes", []):
            raise HTTPException(
                status_code=status.HTTP_403_FORBIDDEN,
                detail=f"Missing required scope: {scope}",
            )

        return current_user

    return dependency


@router.post("/{forecast_id}/rerun", status_code=status.HTTP_202_ACCEPTED)
def rerun_forecast(
    forecast_id: int,
    db: Session = Depends(get_db),
    current_user: dict[str, Any] = Depends(require_scope("forecast:write")),
) -> dict[str, Any]:
    service = ForecastService(db)

    try:
        result = service.rerun_forecast(
            forecast_id=forecast_id,
            requested_by=current_user["user_id"],
        )
    except LookupError:
        raise HTTPException(
            status_code=status.HTTP_404_NOT_FOUND,
            detail="Forecast not found",
        )

    return result
