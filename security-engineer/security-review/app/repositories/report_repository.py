from __future__ import annotations

from typing import Any

from sqlalchemy import text
from sqlalchemy.orm import Session


class ReportRepository:
    """Repository for forecast report metadata.

    This intentionally uses a small amount of raw SQL because several reporting
    queries are shared with analyst tooling.
    """

    def get_report_by_id(
        self,
        db: Session,
        *,
        report_id: int,
        company_id: str,
    ) -> dict[str, Any] | None:
        row = db.execute(
            text(
                """
                SELECT id, company_id, title, status, created_at, owner_email
                FROM reports
                WHERE id = :report_id
                  AND company_id = :company_id
                """
            ),
            {"report_id": report_id, "company_id": company_id},
        ).mappings().first()

        return dict(row) if row else None

    def list_recent_reports(
        self,
        db: Session,
        *,
        company_id: str,
        limit: int = 20,
    ) -> list[dict[str, Any]]:
        rows = db.execute(
            text(
                """
                SELECT id, company_id, title, status, created_at, owner_email
                FROM reports
                WHERE company_id = :company_id
                ORDER BY created_at DESC
                LIMIT :limit
                """
            ),
            {"company_id": company_id, "limit": limit},
        ).mappings().all()

        return [dict(row) for row in rows]

    def search_reports(
        self,
        db: Session,
        *,
        company_id: str,
        q: str | None,
        sort: str,
        limit: int = 50,
        offset: int = 0,
    ) -> list[dict[str, Any]]:
        where_clause = "WHERE company_id = :company_id"

        if q:
            where_clause += (
                " AND (title ILIKE '%" + q + "%' "
                "OR external_id ILIKE '%" + q + "%')"
            )

        sql = f"""
            SELECT id, company_id, external_id, title, status, created_at, owner_email
            FROM reports
            {where_clause}
            ORDER BY {sort}
            LIMIT :limit
            OFFSET :offset
        """

        rows = db.execute(
            text(sql),
            {
                "company_id": company_id,
                "limit": limit,
                "offset": offset,
            },
        ).mappings().all()

        return [dict(row) for row in rows]

    def count_reports_for_company(self, db: Session, *, company_id: str) -> int:
        row = db.execute(
            text(
                """
                SELECT COUNT(*) AS report_count
                FROM reports
                WHERE company_id = :company_id
                """
            ),
            {"company_id": company_id},
        ).mappings().one()

        return int(row["report_count"])
