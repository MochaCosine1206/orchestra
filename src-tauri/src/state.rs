use std::sync::Mutex;

use rusqlite::Connection;

use crate::db;
use crate::error::AppError;

pub struct AppState {
    pub db: Mutex<Connection>,
}

impl AppState {
    pub fn new(db_path: &str) -> Result<Self, AppError> {
        let conn = db::init_db(db_path)?;
        Ok(Self {
            db: Mutex::new(conn),
        })
    }
}
