"""Shared fixtures for orchestrator tests."""

import json
import os
import shutil
import tempfile
from pathlib import Path

import pytest

FIXTURE_DIR = Path(__file__).parent / "fixtures"


@pytest.fixture
def test_config_path():
    """Return path to the 40-issue test config fixture."""
    return FIXTURE_DIR / "test-issues.json"


@pytest.fixture
def tmp_repo(tmp_path):
    """Create a minimal fake git repo in a temp directory."""
    repo = tmp_path / "test-repo"
    repo.mkdir()
    os.system(f"git init {repo} -q && git -C {repo} commit --allow-empty -m 'init' -q")
    return repo


@pytest.fixture
def tmp_config(tmp_path, tmp_repo):
    """Write a copy of the test config pointing to tmp_repo."""
    raw = json.loads((FIXTURE_DIR / "test-issues.json").read_text())
    raw["repo_path"] = str(tmp_repo)
    raw["worktree_base"] = str(tmp_path / "worktrees")
    cfg_path = tmp_path / "test-issues.json"
    cfg_path.write_text(json.dumps(raw, indent=2))
    return cfg_path


@pytest.fixture
def loaded_config(tmp_config):
    """Return a fully-loaded RunConfig from the tmp config."""
    import sys
    sys.path.insert(0, str(Path(__file__).parent.parent))
    from orchestrator.config import load_config
    return load_config(tmp_config)


@pytest.fixture
def state_manager(loaded_config, tmp_path):
    """Return a StateManager with a clean temp state directory."""
    from orchestrator.state import StateManager
    loaded_config.state_dir = tmp_path / "state" / "test-project"
    sm = StateManager(loaded_config)
    sm.ensure_dirs()
    return sm
