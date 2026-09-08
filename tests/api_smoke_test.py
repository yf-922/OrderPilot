# -*- coding: utf-8 -*-
"""
Lightweight API smoke tests for goworks-product.

The current project depends on local MySQL and Redis. To keep the smoke suite
stable before schema/seed scripts are provided, these cases focus on validation
and error responses that do not require fixed database fixtures.
"""

from __future__ import annotations

import json
import os
import sys
import uuid
from dataclasses import dataclass
from typing import Dict, Optional
from urllib.error import HTTPError, URLError
from urllib.parse import urlencode
from urllib.request import Request, urlopen


BASE_URL = os.getenv("BASE_URL", "http://localhost:8080").rstrip("/")
TIMEOUT_SECONDS = float(os.getenv("API_TEST_TIMEOUT", "5"))


@dataclass(frozen=True)
class TestCase:
    case_id: str
    module: str
    scene: str
    method: str
    path: str
    data: Optional[Dict[str, str]]
    expected_status: int
    expected_text: str


TEST_CASES = [
    TestCase(
        "API-001",
        "user",
        "login_missing_user_name",
        "POST",
        "/user/login",
        {"password": "123456"},
        400,
        "userName is required",
    ),
    TestCase(
        "API-002",
        "user",
        "login_missing_password",
        "POST",
        "/user/login",
        {"userName": "test_user"},
        400,
        "password is required",
    ),
    TestCase(
        "API-003",
        "user",
        "create_missing_nick_name",
        "POST",
        "/user/create",
        {"userName": "test_user", "password": "123456"},
        400,
        "nickName is required",
    ),
    TestCase(
        "API-004",
        "user",
        "create_missing_user_name",
        "POST",
        "/user/create",
        {"nickName": "test user", "password": "123456"},
        400,
        "userName is required",
    ),
    TestCase(
        "API-005",
        "product",
        "update_missing_product_id",
        "POST",
        "/product/update",
        {
            "productName": "test product",
            "productImage": "x.png",
            "productUrl": "http://example.com",
            "productNum": "10",
        },
        400,
        "id is required",
    ),
    TestCase(
        "API-006",
        "product",
        "update_invalid_product_num",
        "POST",
        "/product/update",
        {
            "id": "1",
            "productName": "test product",
            "productImage": "x.png",
            "productUrl": "http://example.com",
            "productNum": "abc",
        },
        400,
        "productNum must be int64",
    ),
    TestCase(
        "API-007",
        "order",
        "create_missing_user_id",
        "POST",
        "/order/create",
        {"productID": "1", "orderStatus": "0"},
        400,
        "userID is required",
    ),
    TestCase(
        "API-008",
        "order",
        "create_missing_product_id",
        "POST",
        "/order/create",
        {"userID": "1", "orderStatus": "0"},
        400,
        "productID is required",
    ),
]


def send_request(case: TestCase, token: str) -> tuple[int, str]:
    url = BASE_URL + case.path
    body = None
    headers = {}

    if case.module in ("product", "order"):
        headers["Authorization"] = f"Bearer {token}"
    if case.module == "order":
        headers["Idempotency-Key"] = f"smoke-{uuid.uuid4()}"

    if case.method == "POST":
        body = urlencode(case.data or {}).encode("utf-8")
        headers["Content-Type"] = "application/x-www-form-urlencoded; charset=utf-8"

    req = Request(url, data=body, method=case.method, headers=headers)

    try:
        with urlopen(req, timeout=TIMEOUT_SECONDS) as resp:
            return resp.status, resp.read().decode("utf-8", errors="replace")
    except HTTPError as exc:
        return exc.code, exc.read().decode("utf-8", errors="replace")


def classify_result(case: TestCase, status: int, text: str) -> str:
    if status == case.expected_status and case.expected_text in text:
        return "PASSED"
    if status == 400:
        return "PARAM_ERROR_UNEXPECTED"
    if status in (401, 403):
        return "AUTH_ERROR_UNEXPECTED"
    if status >= 500:
        return "SERVER_ERROR_UNEXPECTED"
    return "ASSERTION_FAILED"


def main() -> int:
    print("goworks-product API smoke test")
    print(f"BASE_URL={BASE_URL}")
    print("-" * 88)

    login_body = urlencode({"userName": "demo", "password": "demo123456"}).encode("utf-8")
    login_request = Request(BASE_URL + "/user/login", data=login_body, method="POST")
    with urlopen(login_request, timeout=TIMEOUT_SECONDS) as response:
        token = json.loads(response.read().decode("utf-8"))["data"]["token"]

    results = []

    for case in TEST_CASES:
        try:
            status, text = send_request(case, token)
            result = classify_result(case, status, text)
        except URLError as exc:
            status = 0
            text = str(exc)
            result = "ENV_UNAVAILABLE"

        ok = result == "PASSED"
        results.append(
            {
                "case_id": case.case_id,
                "module": case.module,
                "scene": case.scene,
                "result": result,
                "status": status,
            }
        )

        mark = "PASS" if ok else "FAIL"
        print(f"[{mark}] {case.case_id} {case.module:<7} {case.method} {case.path:<16} {case.scene} status={status}")
        if not ok:
            print(f"       expected status={case.expected_status}, contains={case.expected_text!r}")
            print(f"       actual result={result}, response={text[:300]!r}")

    passed = sum(1 for item in results if item["result"] == "PASSED")
    failed = len(results) - passed

    print("-" * 88)
    print(json.dumps({"passed": passed, "failed": failed, "total": len(results), "results": results}, ensure_ascii=False))
    return 0 if failed == 0 else 1


if __name__ == "__main__":
    sys.exit(main())
