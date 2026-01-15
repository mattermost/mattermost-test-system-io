//! CLI tool to generate API keys.
//!
//! Usage:
//!   cargo run --bin generate-api-key -- --name "CI - GitHub Actions" --role contributor --expires-in 365d

use std::env;

use rust_report_server::config::Config;
use rust_report_server::db::{migrations, DbPool};
use rust_report_server::models::ApiKeyRole;
use rust_report_server::services::api_key;

fn main() {
    dotenvy::dotenv().ok();

    let args: Vec<String> = env::args().collect();

    // Parse arguments
    let mut name: Option<String> = None;
    let mut role = "contributor".to_string();
    let mut expires_in: Option<String> = None;

    let mut i = 1;
    while i < args.len() {
        match args[i].as_str() {
            "--name" | "-n" => {
                i += 1;
                if i < args.len() {
                    name = Some(args[i].clone());
                }
            }
            "--role" | "-r" => {
                i += 1;
                if i < args.len() {
                    role = args[i].clone();
                }
            }
            "--expires-in" | "-e" => {
                i += 1;
                if i < args.len() {
                    expires_in = Some(args[i].clone());
                }
            }
            "--help" | "-h" => {
                print_usage();
                return;
            }
            _ => {
                eprintln!("Unknown argument: {}", args[i]);
                print_usage();
                std::process::exit(1);
            }
        }
        i += 1;
    }

    // Validate required arguments
    let name = match name {
        Some(n) => n,
        None => {
            eprintln!("Error: --name is required");
            print_usage();
            std::process::exit(1);
        }
    };

    // Parse role
    let role_enum = match ApiKeyRole::parse(&role) {
        Some(r) => r,
        None => {
            eprintln!(
                "Error: Invalid role '{}'. Must be: admin, contributor, viewer",
                role
            );
            std::process::exit(1);
        }
    };

    // Load config and initialize database
    let config = match Config::from_env() {
        Ok(c) => c,
        Err(e) => {
            eprintln!("Error loading config: {}", e);
            std::process::exit(1);
        }
    };

    let pool = match DbPool::new(&config) {
        Ok(p) => p,
        Err(e) => {
            eprintln!("Error connecting to database: {}", e);
            std::process::exit(1);
        }
    };

    // Run migrations
    if let Err(e) = migrations::run_migrations(&pool) {
        eprintln!("Error running migrations: {}", e);
        std::process::exit(1);
    }

    // Generate the key
    let (full_key, api_key) =
        match api_key::create_key(&pool, &name, role_enum, expires_in.as_deref()) {
            Ok(result) => result,
            Err(e) => {
                eprintln!("Error generating key: {}", e);
                std::process::exit(1);
            }
        };

    // Output
    println!();
    println!("════════════════════════════════════════════════════════════════");
    println!("  API Key Generated");
    println!("════════════════════════════════════════════════════════════════");
    println!();
    println!("  ID:      {}", api_key.id);
    println!("  Name:    {}", api_key.name);
    println!("  Role:    {}", api_key.role);
    println!("  Prefix:  {}", api_key.key_prefix);
    if let Some(expires) = api_key.expires_at {
        println!("  Expires: {}", expires.to_rfc3339());
    } else {
        println!("  Expires: Never");
    }
    println!();
    println!("  Key:     {}", full_key);
    println!();
    println!("  ⚠️  Save this key! It cannot be retrieved later.");
    println!("════════════════════════════════════════════════════════════════");
    println!();
}

fn print_usage() {
    eprintln!();
    eprintln!("Usage: generate-api-key --name <name> [--role <role>] [--expires-in <duration>]");
    eprintln!();
    eprintln!("Options:");
    eprintln!("  --name, -n        Name for the API key (required)");
    eprintln!("  --role, -r        Role: admin, contributor, viewer (default: contributor)");
    eprintln!("  --expires-in, -e  Expiration: 30d, 365d, 1y, etc. (default: never)");
    eprintln!("  --help, -h        Show this help");
    eprintln!();
    eprintln!("Examples:");
    eprintln!(
        "  generate-api-key --name \"CI - GitHub Actions\" --role contributor --expires-in 365d"
    );
    eprintln!("  generate-api-key --name \"Admin\" --role admin");
    eprintln!();
}
