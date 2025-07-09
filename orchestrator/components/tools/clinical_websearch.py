from typing import Any
from urllib.parse import urlparse

from langchain_core.callbacks import AsyncCallbackManagerForToolRun, CallbackManagerForToolRun
from langchain_core.tools import BaseTool
from logger import logger
from pydantic import BaseModel, Field

# Import Tavily Search instead of DuckDuckGo
try:
    from langchain_tavily import TavilySearch
    TAVILY_AVAILABLE = True
except ImportError:
    TAVILY_AVAILABLE = False
    logger.warning("langchain_tavily not available. Install with: pip install langchain-tavily")


class ClinicalSearchInput(BaseModel):
    """Input schema for clinical web search."""

    query: str | None = Field(
        None,
        description="Search query for clinical guidelines, medical protocols, or evidence-based medicine information",
    )
    max_results: int = Field(
        default=5, description="Maximum number of search results to return (1-10)", ge=1, le=10
    )
    source_filter: str | None = Field(
        default="medical",
        description="Filter results by source type: 'medical' (default), 'guidelines', 'research', or 'all'",
    )
    dicom_payload: Any | None = Field(None, description="DICOM payload (ignored by this tool)")


class ClinicalWebSearchTool(BaseTool):
    """Tool for searching clinical guidelines and medical information using Tavily Search with medical domain filtering."""

    name: str = "clinical_websearch_tool"
    description: str = (
        "Search for clinical guidelines, medical protocols, treatment recommendations, "
        "and evidence-based medicine information from authoritative medical sources including "
        "PubMed, NIH, WHO, medical journals, and clinical practice guidelines. "
        "Use this tool when you need current medical information, treatment protocols, "
        "clinical guidelines, or evidence-based recommendations that are not in your training data."
    )
    args_schema: type[BaseModel] = ClinicalSearchInput

    def __init__(self, **kwargs):
        """Initialize the ClinicalWebSearchTool."""
        super().__init__(**kwargs)

        if not TAVILY_AVAILABLE:
            raise ImportError("langchain_tavily is required for clinical web search. Install with: pip install langchain-tavily")

        # Initialize Tavily search tool
        self._tavily_search = TavilySearch(
            max_results=10,  # We'll filter these down later
            include_answer=False,
            include_raw_content=False,
            include_images=False,
            search_depth="advanced",  # Use advanced search for better results
        )

        # Define trusted medical domains for filtering and scoring
        self._medical_domains = {
            "pubmed.ncbi.nlm.nih.gov",
            "www.ncbi.nlm.nih.gov",
            "www.nih.gov",
            "www.who.int",
            "www.cdc.gov",
            "www.fda.gov",
            "www.uptodate.com",
            "www.cochrane.org",
            "guidelines.gov",
            "www.nice.org.uk",
            "www.acr.org",
            "www.acc.org",
            "www.aha.org",
            "www.chest.org",
            "www.aafp.org",
            "www.acponline.org",
            "emedicine.medscape.com",
            "www.medscape.com",
            "www.nejm.org",
            "www.thelancet.com",
            "jamanetwork.com",
            "www.bmj.com",
            "www.mayoclinic.org",
            "my.clevelandclinic.org",
            "www.hopkinsmedicine.org",
            "www.mountsinai.org",
            "www.acsm.org",
            "www.asco.org",
            "www.nccn.org",
            "www.escardio.org",
            "www.ersnet.org",
        }

        # Define high-authority medical domains with bonus scores
        self._authority_domains = {
            "pubmed.ncbi.nlm.nih.gov": 20.0,
            "www.cochrane.org": 18.0,
            "www.uptodate.com": 15.0,
            "www.who.int": 12.0,
            "www.nih.gov": 12.0,
            "www.cdc.gov": 10.0,
            "www.nice.org.uk": 10.0,
            "www.nejm.org": 15.0,
            "www.thelancet.com": 15.0,
            "jamanetwork.com": 14.0,
            "www.bmj.com": 13.0,
        }

    def _run(
        self,
        query: str = None,
        max_results: int = 5,
        source_filter: str = "medical",
        run_manager: CallbackManagerForToolRun | None = None,
        dicom_payload: Any = None,  # Accept but ignore DICOM payload
        **kwargs,
    ) -> dict[str, Any]:
        """
        Execute the clinical web search using Tavily Search with medical filtering.

        Args:
            query (str): The search query
            max_results (int): Maximum number of results to return
            source_filter (str): Filter for result sources
            run_manager (Optional[CallbackManagerForToolRun]): Callback manager
            dicom_payload: Ignored - this tool doesn't use DICOM data
            **kwargs: Additional arguments

        Returns:
            Dict[str, Any]: Search results with clinical information
        """
        try:
            # Debug log to verify the query argument
            logger.debug(f"ClinicalWebSearchTool._run received query: {query}")
            # Validate that we have a query
            if not query:
                return self._create_error_response(
                    "Query parameter is required for clinical web search"
                )

            logger.info(f"Executing clinical web search for query: {query}")

            # Enhance query with medical terms for better relevance
            enhanced_query = self._enhance_medical_query(query, source_filter)

            # Perform the Tavily search
            structured_results = self._perform_tavily_search(enhanced_query)

            if not structured_results:
                return self._create_error_response(
                    f"No search results found for query: '{query}'. "
                    f"Try using different keywords or a more general search term."
                )

            # Filter and score results based on medical relevance
            filtered_results = self._filter_and_score_results(structured_results, source_filter)

            # Check if any results remain after filtering
            if not filtered_results:
                return self._create_error_response(
                    f"No relevant medical results found for query: '{query}' with source filter '{source_filter}'. "
                    f"Try using 'all' as the source filter or different search terms."
                )

            # Limit results to requested maximum
            final_results = filtered_results[:max_results]

            return {
                "status": "success",
                "query": query,
                "enhanced_query": enhanced_query,
                "results_count": len(final_results),
                "results": final_results,
                "metadata": {
                    "tool_name": self.name,
                    "search_type": "clinical_web_search",
                    "source_filter": source_filter,
                    "search_engine": "tavily",
                    "timestamp": self._get_timestamp(),
                },
            }

        except Exception as e:
            logger.error(f"Error in clinical web search: {str(e)}")
            return self._create_error_response(str(e))

    def _enhance_medical_query(self, query: str, source_filter: str) -> str:
        """Enhance the search query with medical-specific terms and site restrictions."""
        medical_terms = []
        include_domains = []

        if source_filter == "guidelines":
            medical_terms = ["clinical guidelines", "practice guidelines", "treatment protocol"]
            # Target guideline-specific sites
            include_domains = ["www.nice.org.uk", "guidelines.gov", "www.cochrane.org"]
        elif source_filter == "research":
            medical_terms = [
                "systematic review",
                "meta-analysis",
                "clinical trial",
                "evidence-based",
            ]
            # Target research databases
            include_domains = ["pubmed.ncbi.nlm.nih.gov", "www.cochrane.org"]
        elif source_filter == "medical":
            medical_terms = [
                "clinical",
                "medical",
                "treatment",
                "diagnosis",
                "evidence-based medicine",
            ]
            # Target high-authority medical sites
            include_domains = ["pubmed.ncbi.nlm.nih.gov", "www.uptodate.com", "www.mayoclinic.org"]

        # Build enhanced query with medical context
        enhanced_parts = [query]
        
        # Add medical terms for context
        if medical_terms:
            enhanced_parts.append(f"({' OR '.join(medical_terms)})")

        # Update search tool with domain filtering for this query
        if include_domains and hasattr(self._tavily_search, 'include_domains'):
            self._tavily_search.include_domains = include_domains

        return " ".join(enhanced_parts)

    def _perform_tavily_search(self, query: str) -> list[dict[str, Any]]:
        """Perform Tavily search and return structured results."""
        logger.info(f"Performing Tavily search for: {query}")
        
        try:
            # Use Tavily search tool
            response = self._tavily_search.invoke({"query": query})
            
            # Tavily returns a dict with 'results' key containing the actual results
            if not response or not isinstance(response, dict):
                logger.warning(f"Tavily search returned invalid response format for query: {query}")
                return []
            
            results = response.get('results', [])
            
            # Check if we got valid results
            if not results or not isinstance(results, list):
                logger.warning(f"Tavily search returned no results for query: {query}")
                return []
            
            # Convert to our expected format with source domain extraction
            structured_results = []
            for result in results:
                if isinstance(result, dict):
                    structured_results.append({
                        "title": result.get("title", ""),
                        "url": result.get("url", ""),
                        "source": self._extract_domain(result.get("url", "")),
                        "snippet": result.get("content", "")
                    })
                else:
                    logger.warning(f"Unexpected result format: {type(result)}")
            
            logger.info(f"Successfully retrieved {len(structured_results)} search results")
            return structured_results
            
        except Exception as e:
            error_msg = str(e)
            logger.error(f"Tavily search failed: {error_msg}")
            raise RuntimeError(f"Web search failed: {error_msg}")

    def _filter_and_score_results(
        self, results: list[dict[str, Any]], source_filter: str
    ) -> list[dict[str, Any]]:
        """Filter and score results based on medical relevance."""
        scored_results = []

        for result in results:
            source_domain = result.get("source", "").lower()

            # Check if it's from a trusted medical domain
            is_medical_source = any(domain in source_domain for domain in self._medical_domains)

            # Calculate relevance score
            relevance_score = self._calculate_relevance_score(result, source_filter)

            # Add metadata
            result["is_medical_source"] = is_medical_source
            result["relevance_score"] = relevance_score
            result["source_type"] = self._classify_source_type(source_domain)

            # Only include results with some relevance score
            if relevance_score > 0:
                scored_results.append(result)

        # Sort by relevance score (descending)
        scored_results.sort(key=lambda x: x.get("relevance_score", 0), reverse=True)

        return scored_results

    def _calculate_relevance_score(self, result: dict[str, Any], source_filter: str) -> float:
        """Calculate a relevance score for the search result."""
        score = 0.0

        title = result.get("title", "").lower()
        snippet = result.get("snippet", "").lower()
        source = result.get("source", "").lower()

        # Base score for medical domains
        if any(domain in source for domain in self._medical_domains):
            score += 10.0

        # Bonus for high-authority sources
        for domain, bonus in self._authority_domains.items():
            if domain in source:
                score += bonus
                break

        # Content-based scoring
        clinical_terms = [
            "clinical",
            "guideline",
            "protocol",
            "treatment",
            "diagnosis",
            "evidence",
            "systematic review",
            "medical",
        ]
        for term in clinical_terms:
            if term in title:
                score += 2.0
            if term in snippet:
                score += 1.0

        # Filter-specific bonuses
        if source_filter == "guidelines":
            guideline_terms = ["guideline", "protocol", "recommendation", "practice", "standards"]
            for term in guideline_terms:
                if term in title:
                    score += 3.0
                if term in snippet:
                    score += 1.5

        elif source_filter == "research":
            research_terms = [
                "systematic review",
                "meta-analysis",
                "clinical trial",
                "study",
                "research",
                "pubmed",
            ]
            for term in research_terms:
                if term in title:
                    score += 3.0
                if term in snippet:
                    score += 1.5

        # Penalty for non-medical sources when medical filter is active
        if source_filter == "medical" and not any(
            domain in source for domain in self._medical_domains
        ):
            score *= 0.3  # Reduce score significantly

        return round(score, 1)

    def _classify_source_type(self, domain: str) -> str:
        """Classify the type of medical source."""
        domain = domain.lower()

        if "pubmed" in domain or "ncbi" in domain:
            return "research_database"
        if any(
            gov_domain in domain for gov_domain in ["who.int", "cdc.gov", "nih.gov", "fda.gov"]
        ):
            return "government_health"
        if any(ref_domain in domain for ref_domain in ["uptodate.com", "cochrane.org"]):
            return "clinical_reference"
        if any(guide_domain in domain for guide_domain in ["nice.org.uk", "guidelines.gov"]):
            return "clinical_guidelines"
        if any(
            journal_domain in domain
            for journal_domain in ["nejm.org", "thelancet.com", "jamanetwork.com", "bmj.com"]
        ):
            return "medical_journal"
        if any(
            med_domain in domain
            for med_domain in ["mayoclinic.org", "clevelandclinic.org", "hopkinsmedicine.org"]
        ):
            return "medical_institution"
        return "medical_general"

    def _extract_domain(self, url: str) -> str:
        """Extract domain from URL."""
        try:
            if not url.startswith(("http://", "https://")):
                return url
            parsed = urlparse(url)
            return parsed.netloc.lower()
        except Exception:
            return url.lower()

    def _get_timestamp(self) -> str:
        """Get current timestamp."""
        from datetime import datetime, timezone

        return datetime.now(timezone.utc).isoformat()

    def _create_error_response(self, error_message: str) -> dict[str, Any]:
        """Create a standardized error response."""
        return {
            "status": "error",
            "message": f"Clinical web search error: {error_message}",
            "results": [],
            "metadata": {
                "tool_name": self.name,
                "search_type": "clinical_web_search",
                "search_engine": "tavily",
                "error": error_message,
                "timestamp": self._get_timestamp(),
            },
        }

    async def _arun(
        self,
        query: str = None,
        max_results: int = 5,
        source_filter: str = "medical",
        run_manager: AsyncCallbackManagerForToolRun | None = None,
        dicom_payload: Any = None,  # Accept but ignore DICOM payload
        **kwargs,
    ) -> dict[str, Any]:
        """
        Asynchronously execute the clinical web search.
        Currently calls the synchronous version.
        """
        return self._run(query, max_results, source_filter, run_manager, dicom_payload, **kwargs)
