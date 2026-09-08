use anyhow::Result;
use std::{
    env,
    io::{self, BufRead, BufReader, IsTerminal, Read, Write},
    sync::{Arc, Mutex, mpsc},
    thread::{self, JoinHandle},
    time::{Duration, Instant},
};
use unicode_width::UnicodeWidthStr;

use crate::{Kind, nix::Closure};

pub fn escape_controls(value: &str) -> String {
    let mut escaped = String::with_capacity(value.len());
    for character in value.chars() {
        if character.is_control() {
            escaped.extend(character.escape_default());
        } else {
            escaped.push(character);
        }
    }
    escaped
}

fn color_enabled() -> bool {
    if env::var("NO_COLOR").is_ok_and(|value| !value.is_empty())
        || env::var("TERM").as_deref() == Ok("dumb")
    {
        return false;
    }
    env::var("CLICOLOR_FORCE").is_ok_and(|value| !value.is_empty() && value != "0")
        || io::stdout().is_terminal()
}

fn paint(code: &str, value: &str, color: bool) -> String {
    if color {
        format!("\x1b[{code}m{value}\x1b[0m")
    } else {
        value.to_owned()
    }
}

fn format_duration(duration: Duration) -> String {
    let tenths = (duration.as_secs_f64() * 10.0).round() as u64;
    if tenths < 600 {
        format!("{:.1}s", tenths as f64 / 10.0)
    } else {
        format!("{}m{:.1}s", tenths / 600, (tenths % 600) as f64 / 10.0)
    }
}

fn format_bytes(size: u64) -> String {
    if size < 1024 {
        return format!("{size} B");
    }
    let mut value = size as f64;
    for unit in ["KiB", "MiB", "GiB", "TiB", "PiB", "EiB"] {
        value /= 1024.0;
        if value < 1024.0 || unit == "EiB" {
            return format!("{value:.1} {unit}");
        }
    }
    unreachable!()
}

#[derive(Default)]
struct State {
    action: &'static str,
    started: Option<Instant>,
    rendered: bool,
    enabled: bool,
    diagnostics: Vec<u8>,
}

impl State {
    fn clear(&mut self) {
        if self.rendered {
            let _ = write!(io::stdout(), "\r\x1b[2K");
            let _ = io::stdout().flush();
            self.rendered = false;
        }
    }

    fn render(&mut self) {
        if !self.enabled {
            return;
        }
        let Some(started) = self.started else {
            return;
        };
        let mut status = format!("→ {} {}", self.action, format_duration(started.elapsed()));
        if io::stdout().is_terminal() {
            let width = terminal_size::terminal_size()
                .map_or(80, |(width, _)| width.0 as usize)
                .saturating_sub(1);
            status = status.chars().take(width).collect();
        }
        let _ = write!(
            io::stdout(),
            "\r\x1b[2K{}",
            paint("36", &status, color_enabled())
        );
        let _ = io::stdout().flush();
        self.rendered = true;
    }
}

pub struct Live {
    state: Arc<Mutex<State>>,
    stop: mpsc::Sender<()>,
    thread: Option<JoinHandle<()>>,
}

impl Live {
    pub fn new() -> Self {
        let enabled = env::var("NCR_LIVE").as_deref() == Ok("1")
            || (io::stdout().is_terminal() && env::var("TERM").as_deref() != Ok("dumb"));
        let state = Arc::new(Mutex::new(State {
            enabled,
            ..State::default()
        }));
        let (stop, receiver) = mpsc::channel();
        let thread = enabled.then(|| {
            let worker_state = state.clone();
            thread::spawn(move || {
                while matches!(
                    receiver.recv_timeout(Duration::from_millis(100)),
                    Err(mpsc::RecvTimeoutError::Timeout)
                ) {
                    worker_state.lock().unwrap().render();
                }
            })
        });
        Self {
            state,
            stop,
            thread,
        }
    }

    pub fn set(&self, action: &'static str) {
        let mut state = self.state.lock().unwrap();
        if state.action != action {
            state.started = Some(Instant::now());
        }
        state.action = action;
        state.render();
    }

    pub fn diagnostics(&self, reader: impl Read, label: &str) -> io::Result<()> {
        let label = escape_controls(label);
        let mut reader = BufReader::new(reader);
        let mut line = Vec::new();
        while reader.read_until(b'\n', &mut line)? != 0 {
            let mut state = self.state.lock().unwrap();
            if !label.is_empty() {
                write!(state.diagnostics, "[{label}] ")?;
            }
            state.diagnostics.extend_from_slice(&line);
            if !line.ends_with(b"\n") {
                state.diagnostics.push(b'\n');
            }
            line.clear();
        }
        Ok(())
    }

    pub fn print(mut self, rows: &[Row], hidden: usize) -> Result<()> {
        self.finish();
        print(rows, hidden)?;
        self.state.lock().unwrap().diagnostics.clear();
        Ok(())
    }

    fn finish(&mut self) {
        let _ = self.stop.send(());
        if let Some(thread) = self.thread.take() {
            let _ = thread.join();
        }
        let mut state = self.state.lock().unwrap();
        state.enabled = false;
        state.clear();
    }
}

impl Drop for Live {
    fn drop(&mut self) {
        self.finish();
        let state = self.state.lock().unwrap();
        let _ = io::stderr().write_all(&state.diagnostics);
    }
}

pub struct Row {
    pub kind: Kind,
    pub name: String,
    pub system: String,
    pub duration: Duration,
    pub closure: Option<Closure>,
}

fn print(rows: &[Row], hidden: usize) -> Result<()> {
    let color = color_enabled();
    let show_type = rows.windows(2).any(|pair| pair[0].kind != pair[1].kind);
    let mut table = vec![vec!["host".to_owned()]];
    if show_type {
        table[0].push("type".into());
    }
    table[0].extend(["system", "eval", "closure", "paths"].map(str::to_owned));
    let system_col = if show_type { 2 } else { 1 };
    let eval_col = system_col + 1;
    for row in rows {
        let mut cells = vec![escape_controls(&row.name)];
        if show_type {
            cells.push(row.kind.label.into());
        }
        cells.push(escape_controls(&row.system));
        if let Some(closure) = row.closure {
            cells.extend([
                format_duration(row.duration),
                format_bytes(closure.size),
                closure.paths.to_string(),
            ]);
        } else {
            cells.extend(["—".into(), "—".into(), "—".into()]);
        }
        table.push(cells);
    }
    let mut widths = vec![0; table[0].len()];
    for row in &table {
        for (col, value) in row.iter().enumerate() {
            widths[col] = widths[col].max(value.width());
        }
    }
    widths[system_col] = widths[system_col].max(11);
    widths[eval_col] = widths[eval_col].max(5);
    widths[eval_col + 1] = widths[eval_col + 1].max(8);
    let styles: &[&str] = if show_type {
        &["1", "95", "94", "96", "92", "93"]
    } else {
        &["1", "94", "96", "92", "93"]
    };
    let border = |left: &str, middle: &str, right: &str| {
        paint(
            "90",
            &format!(
                "{left}{}{right}\n",
                widths
                    .iter()
                    .map(|width| "─".repeat(width + 2))
                    .collect::<Vec<_>>()
                    .join(middle)
            ),
            color,
        )
    };
    let mut output = border("╭", "┬", "╮");
    let vertical = paint("90", "│", color);
    for (index, row) in table.iter().enumerate() {
        let header = index == 0;
        let skipped = !header && rows[index - 1].closure.is_none();
        output.push_str(&vertical);
        for (col, value) in row.iter().enumerate() {
            let padding = widths[col] - value.width();
            let style = if header {
                "1;36"
            } else if skipped {
                "2;90"
            } else {
                styles[col]
            };
            let (left, right) = if col < eval_col {
                (0, padding)
            } else if header || skipped {
                (padding / 2, padding - padding / 2)
            } else {
                (padding, 0)
            };
            output.push_str(&format!(
                " {}{}{} {vertical}",
                " ".repeat(left),
                paint(style, value, color),
                " ".repeat(right)
            ));
        }
        output.push('\n');
        if header {
            output.push_str(&border("├", "┼", "┤"));
        }
    }
    output.push_str(&border("╰", "┴", "╯"));
    if hidden > 0 {
        let noun = if hidden == 1 {
            "configuration"
        } else {
            "configurations"
        };
        output.push_str(&paint(
            "2;90",
            &format!("{hidden} other-system {noun} hidden"),
            color,
        ));
        output.push('\n');
    }
    io::stdout().write_all(output.as_bytes())?;
    Ok(())
}
