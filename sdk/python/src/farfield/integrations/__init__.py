"""Optional integrations for agent frameworks with non-OTLP trace APIs."""

from typing import TYPE_CHECKING

if TYPE_CHECKING:
    from .claude_agent_sdk import FarfieldClaudeAgentHooks
    from .openai_agents import FarfieldTracingExporter

__all__ = ["FarfieldClaudeAgentHooks", "FarfieldTracingExporter"]


def __getattr__(name: str) -> object:
    if name == "FarfieldClaudeAgentHooks":
        from .claude_agent_sdk import FarfieldClaudeAgentHooks

        return FarfieldClaudeAgentHooks
    if name == "FarfieldTracingExporter":
        from .openai_agents import FarfieldTracingExporter

        return FarfieldTracingExporter
    raise AttributeError(name)
