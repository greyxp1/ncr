use anyhow::{Context, Result, bail};
use serde::{Deserialize, de::DeserializeOwned};
use serde_json::json;
use std::{
    collections::{BTreeMap, BTreeSet, VecDeque},
    process::{Command, Stdio},
    sync::{Mutex, mpsc},
    thread,
    time::{Duration, Instant},
};

use crate::{Kind, args::Options, report::Live};

#[derive(Deserialize)]
pub struct Discovery {
    pub available: BTreeMap<String, Vec<String>>,
    pub present: bool,
}

#[derive(Deserialize)]
pub struct Configuration {
    pub system: String,
    pub drv: String,
    pub skipped: bool,
}

pub struct Evaluated {
    pub kind: Kind,
    pub name: String,
    pub config: Configuration,
    pub duration: Duration,
}

#[derive(Clone, Copy)]
pub struct Closure {
    pub size: u64,
    pub paths: usize,
}

fn output(args: &[&str], progress: &Live, label: &str) -> Result<Vec<u8>> {
    let mut child = Command::new("nix")
        .env("CLICOLOR_FORCE", "0")
        .env("NO_COLOR", "1")
        .stdin(Stdio::inherit())
        .stdout(Stdio::piped())
        .stderr(Stdio::piped())
        .args(args)
        .spawn()
        .context("start nix")?;
    let stderr = child.stderr.take().unwrap();
    let output = thread::scope(|scope| -> Result<_> {
        let diagnostics = scope.spawn(|| progress.diagnostics(stderr, label));
        let output = child.wait_with_output().context("wait for nix");
        diagnostics
            .join()
            .unwrap()
            .context("read nix diagnostics")?;
        output
    })?;
    if !output.status.success() {
        bail!("nix {} failed: {}", args[0], output.status);
    }
    Ok(output.stdout)
}

pub fn system(progress: &Live) -> Result<String> {
    let bytes = output(&["config", "show", "system"], progress, "")?;
    let system = String::from_utf8(bytes)?.trim().to_owned();
    if system.is_empty() {
        bail!("nix reported an empty system");
    }
    Ok(system)
}

pub fn flake_url(flake: &str, progress: &Live) -> Result<String> {
    #[derive(Deserialize)]
    struct Metadata {
        url: String,
    }
    let metadata: Metadata = serde_json::from_slice(&output(
        &[
            "flake",
            "metadata",
            "--no-update-lock-file",
            "--quiet",
            "--json",
            flake,
        ],
        progress,
        "",
    )?)
    .context("decode flake metadata")?;
    if metadata.url.is_empty() {
        bail!("nix reported an empty flake URL");
    }
    Ok(metadata.url)
}

fn nix_string(value: &str) -> String {
    format!(
        "\"{}\"",
        value
            .replace('\\', "\\\\")
            .replace('"', "\\\"")
            .replace("${", "\\${")
    )
}

pub struct Evaluator<'a> {
    pub opts: &'a Options,
    pub kinds: &'a [Kind],
    pub flake: &'a str,
    pub system: &'a str,
    pub progress: &'a Live,
}

impl Evaluator<'_> {
    pub fn eval<T: DeserializeOwned>(&self, mode: &str, kind: &str, name: &str) -> Result<T> {
        let arguments = json!({
            "flake": self.flake, "enabled": self.kinds.iter().map(|kind| kind.output).collect::<Vec<_>>(),
            "currentSystem": self.system,
            "mode": mode, "kind": kind, "name": name,
        });
        let expression = format!(
            "({}) (builtins.fromJSON {})",
            include_str!("projection.nix"),
            nix_string(&arguments.to_string())
        );
        let label = if mode == "configuration" {
            format!("{kind}.{name}")
        } else {
            String::new()
        };
        serde_json::from_slice(&output(
            &[
                "eval",
                "--impure",
                "--quiet",
                "--json",
                "--expr",
                &expression,
            ],
            self.progress,
            &label,
        )?)
        .context("decode nix evaluation")
    }

    pub fn evaluate(&self, available: &BTreeMap<String, Vec<String>>) -> Result<Vec<Evaluated>> {
        let mut pending = VecDeque::new();
        for &kind in self.kinds {
            let names = &available[kind.output];
            let mut seen = BTreeSet::new();
            let selected = if self.opts.names.is_empty() {
                names
            } else {
                &self.opts.names
            };
            for name in selected {
                if !self.opts.names.is_empty() && (!names.contains(name) || !seen.insert(name)) {
                    continue;
                }
                pending.push_back((kind, name));
            }
        }
        let total = pending.len();
        let pending = Mutex::new(pending);
        thread::scope(|scope| {
            let (sender, receiver) = mpsc::channel();
            for _ in 0..self.opts.jobs.min(total) {
                let sender = sender.clone();
                let pending = &pending;
                scope.spawn(move || {
                    loop {
                        let Some((kind, name)) = pending.lock().unwrap().pop_front() else {
                            break;
                        };
                        let started = Instant::now();
                        let result = self
                            .eval("configuration", kind.output, name)
                            .with_context(|| {
                                format!("evaluate {} configuration {name:?}", kind.label)
                            })
                            .map(|config| Evaluated {
                                kind,
                                name: name.clone(),
                                config,
                                duration: started.elapsed(),
                            });
                        if result.is_err() {
                            pending.lock().unwrap().clear();
                        }
                        if sender.send(result).is_err() {
                            break;
                        }
                    }
                });
            }
            drop(sender);
            let mut result = Vec::with_capacity(total);
            for row in receiver {
                result.push(row?);
            }
            Ok(result)
        })
    }
}

#[derive(Deserialize)]
#[serde(rename_all = "camelCase")]
struct Build {
    drv_path: String,
    outputs: BTreeMap<String, String>,
}

pub fn realise(configs: &[Evaluated], progress: &Live) -> Result<BTreeMap<String, String>> {
    let drvs: BTreeSet<_> = configs
        .iter()
        .filter(|row| !row.config.skipped)
        .map(|row| row.config.drv.as_str())
        .collect();
    let installables: Vec<_> = drvs.iter().map(|drv| format!("{drv}^out")).collect();
    let mut args = vec!["build", "--no-link", "--json", "--quiet"];
    args.extend(installables.iter().map(String::as_str));
    let builds: Vec<Build> = serde_json::from_slice(
        &output(&args, progress, "").context("realise selected configuration closures")?,
    )
    .context("decode realized outputs")?;
    let mut paths = BTreeMap::new();
    for build in builds {
        let path = build
            .outputs
            .get("out")
            .context("realized configuration has no out output")?;
        paths.insert(build.drv_path, path.clone());
    }
    for drv in drvs {
        if !paths.contains_key(drv) {
            bail!("nix did not return an output for {drv}");
        }
    }
    Ok(paths)
}

pub fn closure(path: &str, progress: &Live) -> Result<Closure> {
    // Nix and Lix use different JSON schemas. Their path/size columns share
    // the same format, and store path names cannot contain whitespace.
    let bytes = output(&["path-info", "--recursive", "--size", path], progress, "")?;
    let text = std::str::from_utf8(&bytes).context("decode closure paths")?;
    let mut closure = Closure { size: 0, paths: 0 };
    for line in text.lines() {
        let mut columns = line.split_whitespace();
        columns.next().context("missing closure path")?;
        let nar_size: u64 = columns
            .next()
            .context("missing NAR size")?
            .parse()
            .context("invalid NAR size")?;
        if columns.next().is_some() {
            bail!("invalid nix path-info line {line:?}");
        }
        closure.size = closure
            .size
            .checked_add(nar_size)
            .context("closure size overflow")?;
        closure.paths += 1;
    }
    Ok(closure)
}
