//! Authentication module for API key verification.

mod extractor;

use secrecy::{ExposeSecret, SecretString};

pub use extractor::ApiKeyAuth;

/// Wrapper type for the bootstrap admin key.
/// Uses `SecretString` to prevent accidental logging and zeroize on drop.
///
/// # Security features
/// - `Debug` prints `[REDACTED]` instead of the actual value
/// - Memory is zeroed when dropped (via `zeroize`)
/// - Cannot be accidentally logged or printed
/// - Explicit `.expose_secret()` required to access the value
#[derive(Clone)]
pub struct AdminKey(Option<SecretString>);

impl AdminKey {
    /// Create a new AdminKey from an optional string.
    pub fn new(key: Option<String>) -> Self {
        Self(key.map(SecretString::from))
    }

    /// Securely compare the provided key with the stored admin key.
    /// Uses constant-time comparison to prevent timing attacks.
    pub fn verify(&self, provided: &str) -> bool {
        match &self.0 {
            Some(secret) => {
                let expected = secret.expose_secret();
                // Constant-time comparison
                if expected.len() != provided.len() {
                    return false;
                }
                expected
                    .as_bytes()
                    .iter()
                    .zip(provided.as_bytes())
                    .fold(0u8, |acc, (a, b)| acc | (a ^ b))
                    == 0
            }
            None => false,
        }
    }

    /// Check if an admin key is configured.
    pub fn is_configured(&self) -> bool {
        self.0.is_some()
    }
}

impl std::fmt::Debug for AdminKey {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        match &self.0 {
            Some(_) => write!(f, "AdminKey([REDACTED])"),
            None => write!(f, "AdminKey(None)"),
        }
    }
}
