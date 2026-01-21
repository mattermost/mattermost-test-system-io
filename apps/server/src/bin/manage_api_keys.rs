//! CLI tool to manage API keys.
//!
//! Usage:
//!   cargo run --bin manage-api-keys -- list
//!   cargo run --bin manage-api-keys -- revoke --id <key-id>
//!   cargo run --bin manage-api-keys -- restore --id <key-id>

use std::env;

use tsio::config::Config;
use tsio::db::DbPool;
use tsio::models::ApiKeyListItem;
use tsio::services::api_key;

#[tokio::main]
async fn main() {
    dotenvy::dotenv().ok();

    let args: Vec<String> = env::args().collect();

    if args.len() < 2 {
        print_usage();
        std::process::exit(1);
    }

    let command = &args[1];

    // Initialize database
    let config = match Config::from_env() {
        Ok(c) => c,
        Err(e) => {
            eprintln!("Error loading config: {}", e);
            std::process::exit(1);
        }
    };

    let pool = match DbPool::new(&config).await {
        Ok(p) => p,
        Err(e) => {
            eprintln!("Error connecting to database: {}", e);
            std::process::exit(1);
        }
    };

    match command.as_str() {
        "list" | "ls" => list_keys(&pool).await,
        "revoke" => {
            let id = parse_id_arg(&args);
            revoke_key(&pool, &id).await;
        }
        "restore" => {
            let id = parse_id_arg(&args);
            restore_key(&pool, &id).await;
        }
        "help" | "--help" | "-h" => {
            print_usage();
        }
        _ => {
            eprintln!("Unknown command: {}", command);
            print_usage();
            std::process::exit(1);
        }
    }
}

fn parse_id_arg(args: &[String]) -> String {
    let mut i = 2;
    while i < args.len() {
        if (args[i] == "--id" || args[i] == "-i") && i + 1 < args.len() {
            return args[i + 1].clone();
        }
        i += 1;
    }
    eprintln!("Error: --id is required");
    std::process::exit(1);
}

async fn list_keys(pool: &DbPool) {
    let keys = match api_key::list_keys(pool).await {
        Ok(k) => k,
        Err(e) => {
            eprintln!("Error listing keys: {}", e);
            std::process::exit(1);
        }
    };

    if keys.is_empty() {
        println!("No API keys found.");
        return;
    }

    println!();
    println!(
        "{:<36} {:<12} {:<20} {:<12} {:<10}",
        "ID", "PREFIX", "NAME", "ROLE", "STATUS"
    );
    println!("{}", "─".repeat(92));

    for key in keys {
        let item = ApiKeyListItem::from(key);
        let status = if item.is_revoked { "revoked" } else { "active" };

        // Truncate name if too long
        let name = if item.name.len() > 18 {
            format!("{}...", &item.name[..15])
        } else {
            item.name.clone()
        };

        println!(
            "{:<36} {:<12} {:<20} {:<12} {:<10}",
            item.id, item.key_prefix, name, item.role, status
        );
    }
    println!();
}

async fn revoke_key(pool: &DbPool, id: &str) {
    match api_key::revoke_key(pool, id).await {
        Ok(true) => {
            println!("API key {} revoked successfully.", id);
        }
        Ok(false) => {
            eprintln!("API key {} not found or already revoked.", id);
            std::process::exit(1);
        }
        Err(e) => {
            eprintln!("Error revoking key: {}", e);
            std::process::exit(1);
        }
    }
}

async fn restore_key(pool: &DbPool, id: &str) {
    match api_key::restore_key(pool, id).await {
        Ok(true) => {
            println!("API key {} restored successfully.", id);
        }
        Ok(false) => {
            eprintln!("API key {} not found or not revoked.", id);
            std::process::exit(1);
        }
        Err(e) => {
            eprintln!("Error restoring key: {}", e);
            std::process::exit(1);
        }
    }
}

fn print_usage() {
    eprintln!();
    eprintln!("Usage: manage-api-keys <command> [options]");
    eprintln!();
    eprintln!("Commands:");
    eprintln!("  list, ls              List all API keys");
    eprintln!("  revoke --id <id>      Revoke an API key");
    eprintln!("  restore --id <id>     Restore a revoked API key");
    eprintln!("  help                  Show this help");
    eprintln!();
    eprintln!("Examples:");
    eprintln!("  manage-api-keys list");
    eprintln!("  manage-api-keys revoke --id 550e8400-e29b-41d4-a716-446655440000");
    eprintln!("  manage-api-keys restore --id 550e8400-e29b-41d4-a716-446655440000");
    eprintln!();
}
