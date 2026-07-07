from __future__ import annotations

import os

import typer

from kubewhy.agent import DEFAULT_MODEL, investigate

app = typer.Typer(add_completion=False, no_args_is_help=True)


@app.command()
def ask(
    question: str = typer.Argument(..., help="What do you want to investigate, in plain English."),
    model: str = typer.Option(DEFAULT_MODEL, "--model", "-m", help="Any model slug available on OpenRouter."),
):
    """Ask kubewhy a question about your cluster. Read-only, always."""
    api_key = os.environ.get("OPENROUTER_API_KEY")
    if not api_key:
        typer.secho("OPENROUTER_API_KEY is not set.", fg=typer.colors.RED)
        raise typer.Exit(1)
    investigate(question, api_key=api_key, model=model)


def main() -> None:
    app()


if __name__ == "__main__":
    main()
