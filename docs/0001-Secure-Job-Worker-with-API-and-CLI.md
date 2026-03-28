---
authors: Mark Harris @MarkDHarris (mark.harris@outlook.com)
---

# RFD 0001 - Secure Job Worker Service with API and CLI

## Required Approvers

- Engineering: @rosstimothy, @greedy52, @rhammonds-teleport, @russjones

## What

Provide a secure Linux-based remote job worker service for authenticated users
to start, stop, query status, and stream the output of running arbitrary Linux
processes.

## Why

Users need a secure, lightweight way to execute tasks on remote hosts without
granting full interactive access. SSH is commonly used for this purpose, but it
provides broad, general-purpose access that is difficult to constrain, audit,
and secure. Other approaches, like ad hoc wrapper scripts and container-based
solutions, either inherit the same access concerns or introduce unnecessary
operational complexity.

A lightweight daemon that is purpose-built for remote task execution offers a
more controlled and predictable model. By exposing a narrow interface and
enforcing access via mTLS and certificate-based authorization, the system
reduces the overall attack surface while improving auditability and operational
simplicity. Real-time output streaming further enables visibility into job
execution without requiring interactive access.

## Goals

## Non-Goals


## Design

The implementation separates concerns across three layers:

1. Worker library: process lifecycle, output capture, cgroup management.
2. gRPC-based API server: exposes the library over mTLS with RBAC.
3. CLI client: command-line access to the API.

### Worker Library

### gRPC Server

### CLI Client

### UX
