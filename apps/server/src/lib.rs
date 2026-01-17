//! Rust Report Server library.
//!
//! This library provides the core functionality for the report server,
//! including database operations, authentication, and API services.

pub mod api;
pub mod auth;
pub mod config;
pub mod db;
pub mod entity;
pub mod error;
pub mod middleware;
pub mod migration;
pub mod models;
pub mod services;
