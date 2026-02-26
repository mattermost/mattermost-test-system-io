//! OIDC E2E test suite.
//!
//! Tests GitHub OIDC authentication end-to-end with a mock OIDC provider.
//! Requires a running PostgreSQL database (docker compose -f docker/docker-compose.dev.yml up -d).
//!
//! Run with: cargo test --test oidc_e2e

mod mock_oidc_provider;
mod test_helpers;

mod test_auth_invalid;
mod test_auth_valid;
mod test_claims_storage;
mod test_key_rotation;
mod test_policy_validation;
mod test_role_authz;
