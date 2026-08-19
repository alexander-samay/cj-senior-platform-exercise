from __future__ import annotations

from typing import Any, Generator

from fastapi import APIRouter, Depends, Query
from sqlalchemy.orm import Session

from app.repositories.report_repository import ReportRepository

router = APIRouter(prefix="/api/v1/reports", tags=["reports"])


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


@router.get("/search")
def search_reports(
    q: str | None = Query(default=None, description="Free-text report search"),
    sort: str = Query(default="created_at DESC", description="Sort expression"),
    limit: int = Query(default=50, ge=1, le=100),
    offset: int = Query(default=0, ge=0),
    db: Session = Depends(get_db),
    current_user: dict[str, Any] = Depends(get_current_user),
) -> dict[str, Any]:
    repo = ReportRepository()

    reports = repo.search_reports(
        db,
        company_id=current_user["company_id"],
        q=q,
        sort=sort,
        limit=limit,
        offset=offset,
    )

    return {
        "items": reports,
        "limit": limit,
        "offset": offset,
    }
