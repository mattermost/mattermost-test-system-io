//! Report JSON data model for storing extraction JSON as JSONB.

use chrono::{DateTime, Utc};
use serde::{Deserialize, Serialize};
use uuid::Uuid;

/// Enumeration of supported JSON file types for extraction.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "snake_case")]
#[allow(clippy::enum_variant_names)]
pub enum JsonFileType {
    /// Playwright results.json
    ResultsJson,
    /// Cypress all.json (merged mochawesome)
    AllJson,
    /// Cypress mochawesome.json
    MochawesomeJson,
    /// Detox iOS data file
    IosDataJson,
    /// Detox Android data file
    AndroidDataJson,
}

impl JsonFileType {
    /// Convert to database string representation.
    pub fn as_str(&self) -> &'static str {
        match self {
            JsonFileType::ResultsJson => "results.json",
            JsonFileType::AllJson => "all.json",
            JsonFileType::MochawesomeJson => "mochawesome.json",
            JsonFileType::IosDataJson => "ios-data.json",
            JsonFileType::AndroidDataJson => "android-data.json",
        }
    }

    /// Parse from file type string.
    pub fn parse(s: &str) -> Option<Self> {
        match s {
            "results.json" => Some(JsonFileType::ResultsJson),
            "all.json" => Some(JsonFileType::AllJson),
            "mochawesome.json" => Some(JsonFileType::MochawesomeJson),
            "ios-data.json" => Some(JsonFileType::IosDataJson),
            "android-data.json" => Some(JsonFileType::AndroidDataJson),
            _ => None,
        }
    }

    /// Detect file type from filename path.
    pub fn from_filename(filename: &str) -> Option<Self> {
        if filename.ends_with("results.json") {
            Some(JsonFileType::ResultsJson)
        } else if filename.ends_with("all.json") {
            Some(JsonFileType::AllJson)
        } else if filename.ends_with("mochawesome.json") {
            Some(JsonFileType::MochawesomeJson)
        } else if filename.ends_with("ios-data.json") {
            Some(JsonFileType::IosDataJson)
        } else if filename.ends_with("android-data.json") {
            Some(JsonFileType::AndroidDataJson)
        } else {
            None
        }
    }
}

impl std::fmt::Display for JsonFileType {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        write!(f, "{}", self.as_str())
    }
}

/// Report JSON data stored in the database.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct ReportJson {
    /// Primary key
    pub id: Option<i64>,
    /// Associated report ID
    pub report_id: Uuid,
    /// Type of JSON file
    pub file_type: JsonFileType,
    /// JSON content as serde_json::Value
    pub data: serde_json::Value,
    /// Creation timestamp
    pub created_at: Option<DateTime<Utc>>,
}

impl ReportJson {
    /// Create a new ReportJson instance.
    pub fn new(report_id: Uuid, file_type: JsonFileType, data: serde_json::Value) -> Self {
        Self {
            id: None,
            report_id,
            file_type,
            data,
            created_at: None,
        }
    }
}
