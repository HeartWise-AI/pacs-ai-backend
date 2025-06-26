#!/usr/bin/env python3
"""
Test script for the Clinical Web Search Tool
"""

import os
import sys
from pathlib import Path

# Add the project root to the Python path
project_root = Path(__file__).parent
sys.path.insert(0, str(project_root))

from components.tools.clinical_websearch import ClinicalWebSearchTool


def test_basic_functionality():
    """Test basic tool functionality without API key (uses mock)."""
    print("🔬 Testing Clinical Web Search Tool")
    print("=" * 50)
    
    # Create the tool
    tool = ClinicalWebSearchTool()
    
    print(f"Tool Name: {tool.name}")
    print(f"Tool Description: {tool.description[:100]}...")
    print()
    
    # Test with a clinical query
    test_query = "hypertension treatment guidelines"
    print(f"🔍 Testing search for: '{test_query}'")
    print("-" * 30)
    
    # Run the search (should use mock results since no API key)
    result = tool._run(query=test_query, max_results=3, source_filter="guidelines")
    
    print(f"Status: {result['status']}")
    print(f"Query: {result['query']}")
    print(f"Enhanced Query: {result.get('enhanced_query', 'N/A')}")
    print(f"Results Count: {result['results_count']}")
    print()
    
    # Display results
    if result['results']:
        print("📋 Search Results:")
        for i, res in enumerate(result['results'], 1):
            print(f"\n{i}. {res['title']}")
            print(f"   URL: {res['url']}")
            print(f"   Source: {res['source']} ({res['source_type']})")
            print(f"   Medical Source: {res['is_medical_source']}")
            print(f"   Relevance Score: {res['relevance_score']:.1f}")
            print(f"   Snippet: {res['snippet'][:100]}...")
    
    print("\n" + "=" * 50)
    print("✅ Basic functionality test completed")


def test_different_filters():
    """Test different source filters."""
    print("\n🔍 Testing Different Source Filters")
    print("=" * 50)
    
    tool = ClinicalWebSearchTool()
    test_query = "diabetes management"
    
    filters = ["medical", "guidelines", "research"]
    
    for filter_type in filters:
        print(f"\n📊 Testing filter: {filter_type}")
        print("-" * 20)
        
        result = tool._run(query=test_query, max_results=2, source_filter=filter_type)
        
        print(f"Enhanced Query: {result.get('enhanced_query', 'N/A')}")
        print(f"Results: {result.get('results_count', 0)}")
        
        if result.get('results'):
            for res in result['results']:
                print(f"  - {res['title'][:50]}... (Score: {res['relevance_score']:.1f})")


def test_error_handling():
    """Test error handling scenarios."""
    print("\n⚠️  Testing Error Handling")
    print("=" * 50)
    
    tool = ClinicalWebSearchTool()
    
    # Test with empty query
    result = tool._run(query="", max_results=3)
    print(f"Empty query result: {result['status']}")
    
    # Test with very long query
    long_query = "clinical guidelines for the management of patients with " * 10
    result = tool._run(query=long_query, max_results=3)
    print(f"Long query result: {result['status']}")
    
    print("✅ Error handling test completed")


def main():
    """Run all tests."""
    print("🧪 Clinical Web Search Tool Test Suite")
    print("=" * 60)
    
    # Set up test environment (no API key for mock testing)
    os.environ.pop("SEARCH_API_KEY", None)  # Remove API key to test mock functionality
    
    try:
        test_basic_functionality()
        test_different_filters()
        test_error_handling()
        
        print("\n" + "=" * 60)
        print("🎉 All tests completed successfully!")
        print("\n📝 Next steps:")
        print("   1. Set SEARCH_API_KEY environment variable for real searches")
        print("   2. Choose SEARCH_ENGINE: 'serper' (default) or 'google'")
        print("   3. For Google: also set GOOGLE_CSE_ID environment variable")
        print("   4. Test with real API to verify functionality")
        
    except Exception as e:
        print(f"\n❌ Test failed with error: {str(e)}")
        import traceback
        traceback.print_exc()
        return 1
    
    return 0


if __name__ == "__main__":
    sys.exit(main())