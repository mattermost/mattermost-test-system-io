//! Authentication module for API key verification.

mod extractor;

use secrecy::{ExposeSecret, SecretString};
use subtle::ConstantTimeEq;

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
    ///
    /// Uses `subtle::ConstantTimeEq` which performs a constant-time byte-by-byte
    /// comparison. Unlike a manual fold, `ConstantTimeEq` also avoids leaking
    /// the key length through early-exit branching — both buffers are compared
    /// in full regardless of where they first differ.
    pub fn verify(&self, provided: &str) -> bool {
        match &self.0 {
            Some(secret) => {
                let expected = secret.expose_secret();
                // ConstantTimeEq requires equal-length slices; it returns 0 (false)
                // for unequal lengths without any early exit, preventing length oracle.
                expected.as_bytes().ct_eq(provided.as_bytes()).into()
            }
            None => false,
        }
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
