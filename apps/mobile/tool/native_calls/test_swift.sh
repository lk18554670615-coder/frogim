#!/bin/sh
# Linux-container policy compilation is not an Apple SDK build or device test.
set -eu
swiftc -module-cache-path /tmp/swift-cache -swift-version 5 /native/LinliTenantCallPolicy.swift /tests/TenantCallPolicyTests.swift -o /tmp/tenant-call-tests
/tmp/tenant-call-tests
swiftc -frontend -parse /native/LinliSystemCalls.swift /native/AppDelegate.swift
printf '%s\n' 'PASS: iOS bridge Swift syntax parsed (not Apple SDK type checking)'
